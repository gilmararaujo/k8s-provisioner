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
	exec    executor.CommandExecutor
	address string
}

func NewVaultSecretsOperator(cfg *config.Config, exec executor.CommandExecutor) *VaultSecretsOperator {
	addr := cfg.Vault.Addr
	if addr == "" {
		addr = "http://192.168.56.20:8200"
	}
	return &VaultSecretsOperator{config: cfg, exec: exec, address: addr}
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
	if err := v.waitForVSO(3 * time.Minute); err != nil {
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
	_, err := v.exec.RunShell("curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash")
	return err
}

func (v *VaultSecretsOperator) installVSO() error {
	if _, err := v.exec.RunShell("helm repo add hashicorp https://helm.releases.hashicorp.com 2>/dev/null || true"); err != nil {
		fmt.Printf("Warning: could not add HashiCorp Helm repo: %v\n", err)
	}
	if _, err := v.exec.RunShell("helm repo update hashicorp"); err != nil {
		fmt.Printf("Warning: helm repo update failed: %v\n", err)
	}

	cmd := fmt.Sprintf(
		"helm upgrade --install vault-secrets-operator hashicorp/vault-secrets-operator"+
			" -n vault-secrets-operator-system --create-namespace"+
			" --set defaultVaultConnection.enabled=true"+
			" --set 'defaultVaultConnection.address=%s'"+
			" --wait --timeout=3m",
		v.address,
	)
	_, err := v.exec.RunShell(cmd)
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
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for VSO deployment")
}

func (v *VaultSecretsOperator) createKeycloakResources() error {
	manifest := `apiVersion: v1
kind: Namespace
metadata:
  name: keycloak
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultAuth
metadata:
  name: vault-auth
  namespace: keycloak
spec:
  method: kubernetes
  mount: kubernetes
  kubernetes:
    role: k8s-provisioner
    serviceAccount: default
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: keycloak-admin
  namespace: keycloak
spec:
  vaultAuthRef: vault-auth
  mount: secret
  type: kv-v2
  path: k8s-provisioner/api-keys
  refreshAfter: 30s
  destination:
    name: keycloak-admin
    create: true
    transformation:
      templates:
        username:
          text: '{{- get .Secrets "keycloak_admin_username" -}}'
        password:
          text: '{{- get .Secrets "keycloak_admin_password" -}}'
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: postgres-credentials
  namespace: keycloak
spec:
  vaultAuthRef: vault-auth
  mount: secret
  type: kv-v2
  path: k8s-provisioner/api-keys
  refreshAfter: 30s
  destination:
    name: postgres-credentials
    create: true
    transformation:
      templates:
        username:
          text: '{{- get .Secrets "keycloak_postgres_username" -}}'
        password:
          text: '{{- get .Secrets "keycloak_postgres_password" -}}'`

	if err := executor.WriteFile("/tmp/vso-keycloak.yaml", manifest); err != nil {
		return err
	}
	_, err := v.exec.RunShell("kubectl apply -f /tmp/vso-keycloak.yaml")
	return err
}

func (v *VaultSecretsOperator) createMonitoringResources() error {
	manifest := `apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultAuth
metadata:
  name: vault-auth
  namespace: monitoring
spec:
  method: kubernetes
  mount: kubernetes
  kubernetes:
    role: k8s-provisioner
    serviceAccount: default
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: grafana-admin
  namespace: monitoring
spec:
  vaultAuthRef: vault-auth
  mount: secret
  type: kv-v2
  path: k8s-provisioner/api-keys
  refreshAfter: 30s
  destination:
    name: grafana-admin
    create: true
    transformation:
      templates:
        password:
          text: '{{- get .Secrets "grafana_admin_password" -}}'
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultStaticSecret
metadata:
  name: grafana-oidc
  namespace: monitoring
spec:
  vaultAuthRef: vault-auth
  mount: secret
  type: kv-v2
  path: k8s-provisioner/api-keys
  refreshAfter: 30s
  destination:
    name: grafana-oidc
    create: true
    transformation:
      templates:
        client-secret:
          text: '{{- get .Secrets "keycloak_grafana_client_secret" -}}'`

	if err := executor.WriteFile("/tmp/vso-monitoring.yaml", manifest); err != nil {
		return err
	}
	_, err := v.exec.RunShell("kubectl apply -f /tmp/vso-monitoring.yaml")
	return err
}

func (v *VaultSecretsOperator) createOllamaResources() error {
	manifest := `apiVersion: v1
kind: Namespace
metadata:
  name: ollama
---
apiVersion: secrets.hashicorp.com/v1beta1
kind: VaultAuth
metadata:
  name: vault-auth
  namespace: ollama
spec:
  method: kubernetes
  mount: kubernetes
  kubernetes:
    role: k8s-provisioner
    serviceAccount: default
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
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout: not all secrets synced within %s", timeout)
}

func (v *VaultSecretsOperator) printStatus() {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   Vault Secrets Operator instalado!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("\nRecursos criados:")
	fmt.Println("  VaultStaticSecret/keycloak-admin       → Secret keycloak-admin (keycloak)")
	fmt.Println("  VaultStaticSecret/postgres-credentials → Secret postgres-credentials (keycloak)")
	fmt.Println("  VaultStaticSecret/grafana-admin        → Secret grafana-admin (monitoring)")
	fmt.Println("  VaultStaticSecret/grafana-oidc         → Secret grafana-oidc (monitoring)")
	if v.config.Ollama.APIKey != "" {
		fmt.Println("  VaultStaticSecret/ollama-api-key       → Secret ollama-api-key (ollama)")
	}
	fmt.Println("\nPara verificar o status dos secrets:")
	fmt.Println("  kubectl get vaultstaticsecret -A")
	fmt.Println("  kubectl get secrets -n keycloak")
	fmt.Println("  kubectl get secrets -n monitoring")
	if v.config.Ollama.APIKey != "" {
		fmt.Println("  kubectl get secrets -n ollama")
	}
	fmt.Println(strings.Repeat("=", 50))
}