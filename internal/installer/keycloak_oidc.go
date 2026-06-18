package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

func (k *Keycloak) storeKubeconfigInVault(cpIP, issuerURL string) error {
	token := ResolveVaultToken(k.config.Vault.Token)
	if !k.config.Vault.Enabled || k.config.VaultAddress() == "" || token == "" {
		return fmt.Errorf("vault not configured")
	}

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: k8s-lab
  cluster:
    server: https://%s:%d
    insecure-skip-tls-verify: true
contexts:
- name: k8s-lab
  context:
    cluster: k8s-lab
    user: oidc
current-context: k8s-lab
users:
- name: oidc
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: kubectl
      args:
        - oidc-login
        - get-token
        - --oidc-issuer-url=%s
        - --oidc-client-id=kubectl
        - --oidc-pkce-method=auto
        - --insecure-skip-tls-verify
        - --listen-address=%s
`, cpIP, apiServerPort, issuerURL, kubeloginListenAddr)

	vault := NewVaultClient(k.config.VaultAddress(), token)
	if err := vault.WriteSecret("k8s-provisioner/kubeconfig-oidc", map[string]string{
		"config": kubeconfig,
	}); err != nil {
		return err
	}

	fmt.Println("kubeconfig-oidc stored at: secret/k8s-provisioner/kubeconfig-oidc")
	return nil
}

const apiServerManifest = "/etc/kubernetes/manifests/kube-apiserver.yaml"

func (k *Keycloak) patchAPIServer(issuerURL string) error {
	// Embed the lab CA so the apiserver trusts the self-signed cert that Istio
	// serves for https://keycloak.local. Without certificateAuthority the apiserver
	// cannot fetch the JWKS/OIDC discovery document and every OIDC login fails.
	caPEM, err := k.exec.RunShell(
		"kubectl get secret lab-ca-secret -n cert-manager -o jsonpath='{.data.tls\\.crt}' 2>/dev/null | base64 -d")
	if err != nil || strings.TrimSpace(caPEM) == "" {
		return fmt.Errorf("could not read lab CA (secret lab-ca-secret in cert-manager): %w", err)
	}
	var ca strings.Builder
	for _, line := range strings.Split(strings.TrimRight(caPEM, "\n"), "\n") {
		ca.WriteString("      " + line + "\n")
	}

	authConfig := fmt.Sprintf(`apiVersion: apiserver.config.k8s.io/v1beta1
kind: AuthenticationConfiguration
jwt:
- issuer:
    url: %s
    certificateAuthority: |
%s    audiences:
    - kubectl
    - account
    audienceMatchPolicy: MatchAny
  claimMappings:
    username:
      claim: preferred_username
      prefix: "oidc:"
    groups:
      claim: groups
      prefix: "oidc:"
`, issuerURL, ca.String())

	if err := executor.WriteFile("/etc/kubernetes/pki/auth-config.yaml", authConfig); err != nil {
		return err
	}

	patched := false

	// Make keycloak.local resolvable from inside the apiserver static pod. It runs
	// with hostNetwork, but kubelet still generates the pod's /etc/hosts from
	// hostAliases — the node's /etc/hosts is not used — so the alias must live in
	// the pod spec itself.
	if _, err := k.exec.RunShell(fmt.Sprintf("grep -q 'keycloak.local' %s", apiServerManifest)); err != nil {
		ingressIP := k.ingressIP()
		if ingressIP == "" {
			return fmt.Errorf("could not determine Istio ingress IP to resolve keycloak.local")
		}
		addHostAlias := fmt.Sprintf(
			`sed -i '/^spec:/a\  hostAliases:\n  - ip: "%s"\n    hostnames:\n    - "keycloak.local"' %s`,
			ingressIP, apiServerManifest)
		if _, err := k.exec.RunShell(addHostAlias); err != nil {
			return fmt.Errorf("failed to add hostAliases to apiserver: %w", err)
		}
		patched = true
	}

	// Add the --authentication-config flag if it is not already present. The old
	// check used `grep -c ... || echo 0`, which prints "0\n0" on no match (grep
	// prints its count AND exits non-zero, triggering the `|| echo 0`), so the
	// `!= "0"` test always read the flag as present and silently skipped the patch.
	// grep -q + exit code is unambiguous.
	if _, err := k.exec.RunShell(fmt.Sprintf("grep -q 'authentication-config=' %s", apiServerManifest)); err != nil {
		addFlag := fmt.Sprintf(
			`sed -i '/- kube-apiserver/a\    - --authentication-config=/etc/kubernetes/pki/auth-config.yaml' %s`,
			apiServerManifest)
		if _, err := k.exec.RunShell(addFlag); err != nil {
			return err
		}
		patched = true
	} else {
		fmt.Println("API server already has --authentication-config flag")
	}

	if patched {
		fmt.Println("Waiting for API server to restart with OIDC config...")
		time.Sleep(apiServerRestartWait)

		deadline := time.Now().Add(apiServerHealthTimeout)
		for time.Now().Before(deadline) {
			out, err := k.exec.RunShell("kubectl get --raw='/healthz' 2>/dev/null")
			if err == nil && strings.Contains(out, "ok") {
				fmt.Println("API server is back online!")
				break
			}
			time.Sleep(defaultPollInterval)
		}
	}

	rbac := `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: oidc-k8s-admins
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: Group
  name: "oidc:k8s-admins"
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: oidc-k8s-developers
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: view
subjects:
- kind: Group
  name: "oidc:k8s-developers"
  apiGroup: rbac.authorization.k8s.io`

	if err := executor.WriteFile("/tmp/oidc-rbac.yaml", rbac); err != nil {
		return err
	}

	_, err = k.exec.RunShell("kubectl apply -f /tmp/oidc-rbac.yaml")
	return err
}

// ingressIP returns the IP the apiserver should use to reach keycloak.local. It
// prefers the live Istio ingress LoadBalancer IP and falls back to the first
// address of the configured MetalLB range.
func (k *Keycloak) ingressIP() string {
	out, err := k.exec.RunShell(
		"kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null")
	if err == nil {
		if ip := strings.TrimSpace(out); ip != "" {
			return ip
		}
	}
	if r := k.config.Network.MetalLBRange; r != "" {
		if i := strings.IndexByte(r, '-'); i > 0 {
			return strings.TrimSpace(r[:i])
		}
		return strings.TrimSpace(r)
	}
	return ""
}
