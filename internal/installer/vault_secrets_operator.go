package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type VaultSecretsOperator struct {
	config  *config.Config
	exec    executor.ShellExecutor
	address string
}

func NewVaultSecretsOperator(cfg *config.Config, exec executor.ShellExecutor) *VaultSecretsOperator {
	return &VaultSecretsOperator{config: cfg, exec: exec, address: cfg.VaultAddress()}
}

func (v *VaultSecretsOperator) Install() error {
	fmt.Println("Installing Vault Secrets Operator...")

	if err := v.installHelm(); err != nil {
		return fmt.Errorf("helm installation failed: %w", err)
	}

	if err := v.installVSO(); err != nil {
		return fmt.Errorf("VSO helm install failed: %w", err)
	}

	fmt.Println("Waiting for VSO controller to be ready...")
	if err := v.waitForVSO(shortReadyTimeout); err != nil {
		return fmt.Errorf("VSO did not become ready: %w", err)
	}

	if err := v.createKeycloakResources(); err != nil {
		fmt.Printf("Warning: failed to create Keycloak VSO resources: %v\n", err)
	}
	if err := v.createMonitoringResources(); err != nil {
		fmt.Printf("Warning: failed to create Monitoring VSO resources: %v\n", err)
	}
	if v.config.Ollama.APIKey != "" {
		if err := v.createOllamaResources(); err != nil {
			fmt.Printf("Warning: failed to create Ollama VSO resources: %v\n", err)
		}
	}

	fmt.Println("Waiting for secrets to sync from Vault...")
	if err := v.waitForSecrets(2 * time.Minute); err != nil {
		fmt.Printf("Warning: secrets may not have fully synced yet: %v\n", err)
	}

	v.printStatus()
	return nil
}

func (v *VaultSecretsOperator) installHelm() error {
	if _, err := v.exec.RunShell("helm version 2>/dev/null"); err == nil {
		return nil
	}
	fmt.Println("Installing Helm...")
	_, err := v.exec.RunShell("curl -fsSL --connect-timeout 10 --max-time 300 https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash")
	return err
}

// vsoNamespace is where the operator and its default VaultConnection live.
const vsoNamespace = "vault-secrets-operator-system"

// vaultCASecret holds the Vault CA (key ca.crt) that the default VaultConnection
// uses to verify Vault's TLS certificate. The chart requires it to exist before
// the connection is created.
const vaultCASecret = "vault-ca"

// createVaultCASecret publishes the Vault CA to the operator namespace. Returns
// false (no error) for a plain-HTTP Vault, where no CA is needed.
func (v *VaultSecretsOperator) createVaultCASecret() (bool, error) {
	if !strings.HasPrefix(v.address, "https://") {
		return false, nil
	}
	if err := ensureVaultCA(v.config, v.exec); err != nil {
		return false, err
	}
	pem, err := executor.ReadFileContents(v.config.VaultCACert())
	if err != nil {
		return false, fmt.Errorf("read vault CA: %w", err)
	}
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %s
---
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  ca.crt: %q`, vsoNamespace, vaultCASecret, vsoNamespace, pem)
	if _, err := v.exec.RunShellWithStdin("kubectl apply -f -", manifest); err != nil {
		return false, fmt.Errorf("create %s secret: %w", vaultCASecret, err)
	}
	return true, nil
}

func (v *VaultSecretsOperator) installVSO() error {
	withCA, err := v.createVaultCASecret()
	if err != nil {
		return err
	}

	if _, err := v.exec.RunShell("helm repo add hashicorp https://helm.releases.hashicorp.com 2>/dev/null || true"); err != nil {
		fmt.Printf("Warning: could not add HashiCorp Helm repo: %v\n", err)
	}
	if _, err := v.exec.RunShell("helm repo update hashicorp"); err != nil {
		fmt.Printf("Warning: helm repo update failed: %v\n", err)
	}

	cmd := fmt.Sprintf(
		"helm upgrade --install vault-secrets-operator hashicorp/vault-secrets-operator"+
			" -n %s --create-namespace"+
			" --set defaultVaultConnection.enabled=true"+
			" --set 'defaultVaultConnection.address=%s'",
		vsoNamespace, v.address,
	)
	if withCA {
		cmd += " --set defaultVaultConnection.caCertSecret=" + vaultCASecret
	}
	_, err = v.exec.RunShell(cmd + " --wait --timeout=3m")
	return err
}

func (v *VaultSecretsOperator) waitForVSO(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := v.exec.RunShell(
			"kubectl get deployment vault-secrets-operator-controller-manager" +
				" -n vault-secrets-operator-system -o jsonpath='{.status.readyReplicas}' 2>/dev/null",
		)
		if err == nil && strings.TrimSpace(out) == "1" {
			return nil
		}
		fmt.Println("Waiting for VSO controller...")
		time.Sleep(defaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for VSO deployment")
}

// vsoNamespaceAndAuth returns the Namespace + VaultAuth preamble shared by every
// VSO manifest: it creates ns and a kubernetes-auth VaultAuth named vault-auth
// that the VaultStaticSecret resources in that namespace reference via vaultAuthRef.
func vsoNamespaceAndAuth(ns string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultAuth
metadata:
  name: vault-auth
  namespace: %[1]s
spec:
  method: kubernetes
  mount: kubernetes
  kubernetes:
    role: k8s-provisioner
    serviceAccount: default`, ns)
}

// credentialSecret renders a VaultStaticSecret that mirrors one login path
// (see labCredential) into a K8s Secret with the same plain keys, e.g.
// username/password. excludeRaw drops VSO's _raw copy of the KV response.
func credentialSecret(name, ns string, c labCredential) string {
	return fmt.Sprintf(`
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  vaultAuthRef: vault-auth
  mount: secret
  type: kv-v2
  path: %[3]s
  refreshAfter: 30s
  destination:
    name: %[1]s
    create: true
    overwrite: true
    transformation:
      excludeRaw: true`, name, ns, c.path)
}

func (v *VaultSecretsOperator) createKeycloakResources() error {
	manifest := vsoNamespaceAndAuth("keycloak") +
		credentialSecret("keycloak-admin", "keycloak", keycloakAdminCredential) +
		credentialSecret("postgres-credentials", "keycloak", keycloakPostgresCredential)

	if err := executor.WriteFile("/tmp/vso-keycloak.yaml", manifest); err != nil {
		return err
	}
	_, err := v.exec.RunShell("kubectl apply -f /tmp/vso-keycloak.yaml")
	return err
}

func (v *VaultSecretsOperator) createMonitoringResources() error {
	manifest := vsoNamespaceAndAuth("monitoring") +
		credentialSecret("grafana-admin", "monitoring", grafanaAdminCredential) +
		credentialSecret("grafana-oidc", "monitoring", keycloakGrafanaClientCredential)

	if err := executor.WriteFile("/tmp/vso-monitoring.yaml", manifest); err != nil {
		return err
	}
	_, err := v.exec.RunShell("kubectl apply -f /tmp/vso-monitoring.yaml")
	return err
}

func (v *VaultSecretsOperator) createOllamaResources() error {
	manifest := vsoNamespaceAndAuth("ollama") + `
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: ollama-api-key
  namespace: ollama
spec:
  vaultAuthRef: vault-auth
  mount: secret
  type: kv-v2
  path: k8s-provisioner/api-keys
  refreshAfter: 30s
  destination:
    name: ollama-api-key
    create: true
    transformation:
      excludeRaw: true
      excludes: [".*"]
      templates:
        api-key:
          text: '{{- get .Secrets "ollama_api_key" -}}'`

	if err := executor.WriteFile("/tmp/vso-ollama.yaml", manifest); err != nil {
		return err
	}
	_, err := v.exec.RunShell("kubectl apply -f /tmp/vso-ollama.yaml")
	return err
}

func (v *VaultSecretsOperator) waitForSecrets(timeout time.Duration) error {
	checks := []string{
		"kubectl get secret keycloak-admin -n keycloak 2>/dev/null",
		"kubectl get secret postgres-credentials -n keycloak 2>/dev/null",
		"kubectl get secret grafana-admin -n monitoring 2>/dev/null",
		"kubectl get secret grafana-oidc -n monitoring 2>/dev/null",
	}
	if v.config.Ollama.APIKey != "" {
		checks = append(checks, "kubectl get secret ollama-api-key -n ollama 2>/dev/null")
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReady := true
		for _, cmd := range checks {
			if out, _ := v.exec.RunShell(cmd); out == "" {
				allReady = false
				break
			}
		}
		if allReady {
			fmt.Println("All secrets synced from Vault!")
			return nil
		}
		fmt.Println("Waiting for secrets to sync...")
		time.Sleep(defaultPollInterval)
	}
	return fmt.Errorf("timeout: not all secrets synced within %s", timeout)
}

func (v *VaultSecretsOperator) printStatus() {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   Vault Secrets Operator installed!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("\nResources created:")
	fmt.Println("  VaultStaticSecret/keycloak-admin       → Secret keycloak-admin (keycloak)")
	fmt.Println("  VaultStaticSecret/postgres-credentials → Secret postgres-credentials (keycloak)")
	fmt.Println("  VaultStaticSecret/grafana-admin        → Secret grafana-admin (monitoring)")
	fmt.Println("  VaultStaticSecret/grafana-oidc         → Secret grafana-oidc (monitoring)")
	if v.config.Ollama.APIKey != "" {
		fmt.Println("  VaultStaticSecret/ollama-api-key       → Secret ollama-api-key (ollama)")
	}
	fmt.Println("\nTo check the status of the secrets:")
	fmt.Println("  kubectl get vaultstaticsecret -A")
	fmt.Println("  kubectl get secrets -n keycloak")
	fmt.Println("  kubectl get secrets -n monitoring")
	if v.config.Ollama.APIKey != "" {
		fmt.Println("  kubectl get secrets -n ollama")
	}
	fmt.Println(strings.Repeat("=", 50))
}
