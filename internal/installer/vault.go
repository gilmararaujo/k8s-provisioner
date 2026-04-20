package installer

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type VaultInstaller struct {
	config  *config.Config
	exec    executor.CommandExecutor
	address string
}

func NewVault(cfg *config.Config, exec executor.CommandExecutor) *VaultInstaller {
	addr := cfg.Vault.Address
	if addr == "" {
		addr = "http://192.168.56.20:8200"
	}
	return &VaultInstaller{config: cfg, exec: exec, address: addr}
}

type vaultInitRequest struct {
	SecretShares    int `json:"secret_shares"`
	SecretThreshold int `json:"secret_threshold"`
}

type vaultInitResponse struct {
	Keys      []string `json:"keys"`
	RootToken string   `json:"root_token"`
}

func (v *VaultInstaller) Install() error {
	fmt.Println("Configuring HashiCorp Vault on storage node...")

	if err := v.waitForVault(3 * time.Minute); err != nil {
		return fmt.Errorf("vault not reachable at %s: %w", v.address, err)
	}

	initialized, err := v.isInitialized()
	if err != nil {
		return fmt.Errorf("failed to check vault status: %w", err)
	}

	var rootToken string
	if !initialized {
		fmt.Println("Initializing Vault...")
		rootToken, err = v.initialize()
		if err != nil {
			return fmt.Errorf("vault initialization failed: %w", err)
		}
		fmt.Println("Vault initialized and unsealed successfully")
	} else {
		fmt.Println("Vault already initialized, loading stored credentials...")
		rootToken, err = v.loadRootToken()
		if err != nil {
			return fmt.Errorf("failed to load vault root token: %w", err)
		}
	}

	fmt.Println("Enabling KV v2 secrets engine...")
	if err := v.enableKVSecrets(rootToken); err != nil {
		fmt.Printf("Warning: failed to enable KV secrets engine: %v\n", err)
	}

	if v.config.Vault.K8sAuth {
		fmt.Println("Configuring Kubernetes auth method...")
		if err := v.configureK8sAuth(rootToken); err != nil {
			fmt.Printf("Warning: failed to configure k8s auth: %v\n", err)
		}
	}

	fmt.Println("Storing API secrets in Vault...")
	if err := v.storeAPISecrets(rootToken); err != nil {
		fmt.Printf("Warning: failed to store API secrets: %v\n", err)
	}

	v.printAccessInfo()
	return nil
}

func (v *VaultInstaller) waitForVault(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(v.address + "/v1/sys/health")
		if err == nil {
			resp.Body.Close()
			// 200=active, 429=standby, 501=not initialized, 503=sealed — all mean API is up
			if resp.StatusCode != 0 {
				return nil
			}
		}
		fmt.Printf("Waiting for Vault at %s...\n", v.address)
		time.Sleep(ShortPollInterval)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func (v *VaultInstaller) isInitialized() (bool, error) {
	resp, err := http.Get(v.address + "/v1/sys/init")
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}

	init, _ := result["initialized"].(bool)
	return init, nil
}

func (v *VaultInstaller) initialize() (string, error) {
	initReq := vaultInitRequest{SecretShares: 5, SecretThreshold: 3}
	initResp, err := v.vaultPost("/v1/sys/init", "", initReq)
	if err != nil {
		return "", fmt.Errorf("init request failed: %w", err)
	}

	keys, _ := initResp["keys"].([]interface{})
	rootToken, _ := initResp["root_token"].(string)

	if len(keys) == 0 || rootToken == "" {
		return "", fmt.Errorf("unexpected init response: %v", initResp)
	}

	// Unseal with first 3 keys (threshold=3)
	fmt.Println("Unsealing Vault...")
	for i := 0; i < 3; i++ {
		key, _ := keys[i].(string)
		if _, err := v.vaultPut("/v1/sys/unseal", "", map[string]interface{}{"key": key}); err != nil {
			return "", fmt.Errorf("unseal attempt %d failed: %w", i+1, err)
		}
	}

	// Persist init data on controlplane
	initData := vaultInitResponse{RootToken: rootToken}
	for _, k := range keys {
		if s, ok := k.(string); ok {
			initData.Keys = append(initData.Keys, s)
		}
	}
	if err := v.saveInitData(initData); err != nil {
		fmt.Printf("Warning: failed to save vault init data: %v\n", err)
	}

	return rootToken, nil
}

func (v *VaultInstaller) loadRootToken() (string, error) {
	// Try reading from storage node via SSH
	storageIP := "192.168.56.20"
	out, err := v.exec.RunShell(fmt.Sprintf(
		"sshpass -p 'vagrant' ssh -o StrictHostKeyChecking=no vagrant@%s 'sudo cat /etc/vault.d/vault-init.json'",
		storageIP,
	))
	if err == nil && out != "" {
		var init vaultInitResponse
		if jsonErr := json.Unmarshal([]byte(out), &init); jsonErr == nil && init.RootToken != "" {
			return init.RootToken, nil
		}
	}

	// Fallback: local controlplane copy
	data, err := os.ReadFile("/etc/k8s-provisioner/vault-init.json")
	if err != nil {
		return "", fmt.Errorf("vault-init.json not found on storage node or controlplane")
	}
	var init vaultInitResponse
	if err := json.Unmarshal(data, &init); err != nil {
		return "", err
	}
	if init.RootToken == "" {
		return "", fmt.Errorf("root_token is empty in vault-init.json")
	}
	return init.RootToken, nil
}

func (v *VaultInstaller) saveInitData(data vaultInitResponse) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	// Always save locally on controlplane as reliable fallback
	localPath := "/etc/k8s-provisioner/vault-init.json"
	if err := os.WriteFile(localPath, raw, 0600); err != nil {
		return fmt.Errorf("failed to save vault-init.json locally: %w", err)
	}

	// Copy to storage node via scp (avoids shell-escaping issues with JSON)
	storageIP := "192.168.56.20"
	if _, err := v.exec.RunShell("apt-get install -y sshpass 2>/dev/null || true"); err != nil {
		fmt.Printf("Warning: could not install sshpass: %v\n", err)
	}

	scpCmd := fmt.Sprintf(
		"sshpass -p 'vagrant' scp -o StrictHostKeyChecking=no %s vagrant@%s:/tmp/vault-init.json",
		localPath, storageIP,
	)
	moveCmd := fmt.Sprintf(
		"sshpass -p 'vagrant' ssh -o StrictHostKeyChecking=no vagrant@%s "+
			"'sudo mv /tmp/vault-init.json /etc/vault.d/vault-init.json && sudo chmod 600 /etc/vault.d/vault-init.json'",
		storageIP,
	)

	if _, err := v.exec.RunShell(scpCmd); err != nil {
		fmt.Printf("Warning: could not scp vault-init.json to storage node: %v\n", err)
		fmt.Printf("Vault init data saved locally at %s\n", localPath)
		return nil
	}
	if _, err := v.exec.RunShell(moveCmd); err != nil {
		fmt.Printf("Warning: could not move vault-init.json on storage node: %v\n", err)
	}

	fmt.Printf("Vault init data saved to %s:/etc/vault.d/vault-init.json\n", storageIP)
	fmt.Printf("Backup local em: %s\n", localPath)
	return nil
}

func (v *VaultInstaller) enableKVSecrets(token string) error {
	// Check if already mounted
	mounts, err := v.vaultGet("/v1/sys/mounts", token)
	if err == nil {
		if _, exists := mounts["secret/"]; exists {
			fmt.Println("KV v2 secrets engine already enabled")
			return nil
		}
	}

	_, err = v.vaultPost("/v1/sys/mounts/secret", token, map[string]interface{}{
		"type":    "kv",
		"options": map[string]string{"version": "2"},
	})
	return err
}

func (v *VaultInstaller) configureK8sAuth(token string) error {
	// Enable kubernetes auth method
	if _, err := v.vaultPost("/v1/sys/auth/kubernetes", token, map[string]interface{}{
		"type": "kubernetes",
	}); err != nil && !strings.Contains(err.Error(), "path is already in use") {
		return fmt.Errorf("enable k8s auth: %w", err)
	}

	// Read Kubernetes CA cert from controlplane
	caCert, err := os.ReadFile("/etc/kubernetes/pki/ca.crt")
	if err != nil {
		return fmt.Errorf("read k8s CA cert: %w", err)
	}

	// Create vault-auth ServiceAccount
	saManifest := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: vault-auth
  namespace: kube-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: vault-auth-tokenreview
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
- kind: ServiceAccount
  name: vault-auth
  namespace: kube-system`

	if err := executor.WriteFile("/tmp/vault-auth-sa.yaml", saManifest); err != nil {
		return err
	}
	if _, err := v.exec.RunShell("kubectl apply -f /tmp/vault-auth-sa.yaml"); err != nil {
		return fmt.Errorf("create vault-auth SA: %w", err)
	}

	// Create a bound token for vault-auth SA
	saToken, err := v.exec.RunShell(
		"kubectl create token vault-auth -n kube-system --duration=8760h",
	)
	if err != nil {
		return fmt.Errorf("create SA token: %w", err)
	}

	k8sHost := fmt.Sprintf("https://%s:6443", v.config.Network.ControlPlaneIP)
	if _, err := v.vaultPost("/v1/auth/kubernetes/config", token, map[string]interface{}{
		"kubernetes_host":    k8sHost,
		"kubernetes_ca_cert": string(caCert),
		"token_reviewer_jwt": strings.TrimSpace(saToken),
	}); err != nil {
		return fmt.Errorf("configure k8s auth backend: %w", err)
	}

	// Create read-only policy for k8s workloads
	policy := `path "secret/data/k8s-provisioner/*" { capabilities = ["read"] }`
	if _, err := v.vaultPut("/v1/sys/policies/acl/k8s-provisioner", token, map[string]interface{}{
		"policy": policy,
	}); err != nil {
		return fmt.Errorf("create policy: %w", err)
	}

	// Create role bound to all namespaces
	if _, err := v.vaultPost("/v1/auth/kubernetes/role/k8s-provisioner", token, map[string]interface{}{
		"bound_service_account_names":      []string{"default", "vault-auth"},
		"bound_service_account_namespaces": []string{"*"},
		"policies":                         []string{"k8s-provisioner"},
		"ttl":                              "24h",
	}); err != nil {
		return fmt.Errorf("create k8s role: %w", err)
	}

	fmt.Println("Kubernetes auth method configured successfully")
	return nil
}

func (v *VaultInstaller) storeAPISecrets(token string) error {
	secrets := map[string]string{}

	if v.config.Ollama.APIKey != "" {
		secrets["ollama_api_key"] = v.config.Ollama.APIKey
	}
	if v.config.KarporAI.AuthToken != "" {
		secrets["karpor_auth_token"] = v.config.KarporAI.AuthToken
	}

	// Gera senha do Grafana se ainda não existe no Vault
	grafanaPassword, err := v.resolveOrGenerate(token, "grafana_admin_password")
	if err != nil {
		fmt.Printf("Warning: could not resolve grafana password: %v\n", err)
		grafanaPassword = "admin123" // fallback para lab
	}
	secrets["grafana_admin_password"] = grafanaPassword

	_, err = v.vaultPost("/v1/secret/data/k8s-provisioner/api-keys", token, map[string]interface{}{
		"data": secrets,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Stored %d secret(s) at secret/data/k8s-provisioner/api-keys\n", len(secrets))
	fmt.Printf("Grafana admin password stored in Vault (grafana_admin_password)\n")
	return nil
}

// resolveOrGenerate retorna o valor existente no Vault ou gera um novo.
func (v *VaultInstaller) resolveOrGenerate(token, key string) (string, error) {
	existing, err := v.vaultGet("/v1/secret/data/k8s-provisioner/api-keys", token)
	if err == nil {
		if data, ok := existing["data"].(map[string]interface{}); ok {
			if inner, ok := data["data"].(map[string]interface{}); ok {
				if val, ok := inner[key].(string); ok && val != "" {
					return val, nil
				}
			}
		}
	}
	return generatePassword(16)
}

func generatePassword(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", err
		}
		result[i] = chars[n.Int64()]
	}
	return string(result), nil
}

// vaultGet performs an authenticated GET to the Vault API.
func (v *VaultInstaller) vaultGet(path, token string) (map[string]interface{}, error) {
	return v.vaultRequest("GET", path, token, nil)
}

// vaultPost performs an authenticated POST to the Vault API.
func (v *VaultInstaller) vaultPost(path, token string, body interface{}) (map[string]interface{}, error) {
	return v.vaultRequest("POST", path, token, body)
}

// vaultPut performs an authenticated PUT to the Vault API.
func (v *VaultInstaller) vaultPut(path, token string, body interface{}) (map[string]interface{}, error) {
	return v.vaultRequest("PUT", path, token, body)
}

func (v *VaultInstaller) vaultRequest(method, path, token string, body interface{}) (map[string]interface{}, error) {
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, v.address+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Vault-Token", token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("vault %s %s returned %d: %s", method, path, resp.StatusCode, string(respBody))
	}

	if len(respBody) == 0 {
		return map[string]interface{}{}, nil
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// FetchSecret lê um secret do Vault KV v2 em secret/k8s-provisioner/api-keys.
// Pode ser chamada por outros installers quando vault.enabled=true.
func FetchSecret(cfg *config.Config, key string) (string, error) {
	addr := cfg.Vault.Address
	if addr == "" {
		addr = "http://192.168.56.20:8200"
	}

	token, err := readLocalRootToken()
	if err != nil {
		return "", fmt.Errorf("vault token unavailable: %w", err)
	}

	req, err := http.NewRequest("GET", addr+"/v1/secret/data/k8s-provisioner/api-keys", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return "", nil // secret path not found yet
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vault returned %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	data, _ := result["data"].(map[string]interface{})
	innerData, _ := data["data"].(map[string]interface{})
	value, _ := innerData[key].(string)
	return value, nil
}

// readLocalRootToken lê o root token do arquivo salvo no controlplane.
func readLocalRootToken() (string, error) {
	data, err := os.ReadFile("/etc/k8s-provisioner/vault-init.json")
	if err != nil {
		return "", fmt.Errorf("vault-init.json not found at /etc/k8s-provisioner/vault-init.json")
	}
	var init vaultInitResponse
	if err := json.Unmarshal(data, &init); err != nil {
		return "", err
	}
	if init.RootToken == "" {
		return "", fmt.Errorf("root_token is empty in vault-init.json")
	}
	return init.RootToken, nil
}

func (v *VaultInstaller) printAccessInfo() {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   HashiCorp Vault configurado com sucesso!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("\nVault UI:  %s/ui\n", v.address)
	fmt.Printf("Vault API: %s\n", v.address)
	fmt.Println("\nCredenciais salvas em: /etc/k8s-provisioner/vault-init.json")
	fmt.Println("\nPara usar o Vault:")
	fmt.Printf("  export VAULT_ADDR=%s\n", v.address)
	fmt.Println("  export VAULT_TOKEN=$(cat /etc/k8s-provisioner/vault-init.json | jq -r .root_token)")
	fmt.Println("\nPara ler todos os secrets:")
	fmt.Println("  vault kv get secret/k8s-provisioner/api-keys")
	fmt.Println("\nSenha do Grafana:")
	fmt.Println("  vault kv get -field=grafana_admin_password secret/k8s-provisioner/api-keys")
	fmt.Println("\nAutenticação Kubernetes (em pods):")
	fmt.Println("  vault write auth/kubernetes/login role=k8s-provisioner jwt=$SA_TOKEN")
	fmt.Println(strings.Repeat("=", 50))
}
