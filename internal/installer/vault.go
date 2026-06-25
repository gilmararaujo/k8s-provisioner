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

// vaultHTTPClient bounds the Vault bootstrap calls (health/init/unseal/KV setup)
// so a hung Vault on a freshly-booted storage node fails fast instead of blocking
// the deadline loops indefinitely (RES-1).
var vaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

type VaultInstaller struct {
	config  *config.Config
	exec    executor.ShellExecutor
	address string
}

func NewVaultInstaller(cfg *config.Config, exec executor.ShellExecutor) *VaultInstaller {
	return &VaultInstaller{config: cfg, exec: exec, address: cfg.VaultAddress()}
}

type vaultInitRequest struct {
	SecretShares    int `json:"secret_shares"`
	SecretThreshold int `json:"secret_threshold"`
}

type vaultInitResponse struct {
	Keys             []string `json:"keys"`
	RootToken        string   `json:"root_token"`
	ProvisionerToken string   `json:"provisioner_token,omitempty"`
}

func (v *VaultInstaller) Install() error {
	fmt.Println("Configuring HashiCorp Vault on storage node...")

	if err := v.waitForVault(vaultReadyTimeout); err != nil {
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

	if true {
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
		resp, err := vaultHTTPClient.Get(v.address + "/v1/sys/health")
		if err == nil {
			if closeErr := resp.Body.Close(); closeErr != nil {
				fmt.Printf("Warning: failed to close response body: %v\n", closeErr)
			}
			// 200=active, 429=standby, 501=not initialized, 503=sealed — all mean API is up
			if resp.StatusCode != 0 {
				return nil
			}
		}
		fmt.Printf("Waiting for Vault at %s...\n", v.address)
		time.Sleep(shortPollInterval)
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func (v *VaultInstaller) isInitialized() (bool, error) {
	resp, err := vaultHTTPClient.Get(v.address + "/v1/sys/init")
	if err != nil {
		return false, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Warning: failed to close response body: %v\n", err)
		}
	}()

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

	// Mint a scoped, KV-only token for installers and the `vault` CLI so the
	// all-powerful root token is not what every component authenticates with. If
	// this fails we fall back to the root token (ResolveVaultToken prefers the
	// scoped token but falls back), so a failure here is non-fatal.
	provToken, perr := v.createProvisionerToken(rootToken)
	if perr != nil {
		fmt.Printf("Warning: scoped provisioner token not created (%v) — components will use the root token\n", perr)
	}

	// Persist init data on controlplane
	initData := vaultInitResponse{RootToken: rootToken, ProvisionerToken: provToken}
	for _, k := range keys {
		if s, ok := k.(string); ok {
			initData.Keys = append(initData.Keys, s)
		}
	}
	// Persisting the freshly generated unseal keys + root token is the one step
	// that must not be downgraded to a warning: these secrets cannot be
	// regenerated, and without them Vault is unrecoverable once it seals. Dump
	// them to stdout as a last-resort capture, then fail hard.
	if err := v.saveInitData(initData); err != nil {
		fmt.Println("\n!!! CRITICAL: could not persist Vault init data !!!")
		fmt.Println("!!! Save the following NOW or Vault becomes unrecoverable: !!!")
		fmt.Printf("root_token: %s\n", rootToken)
		for i, k := range initData.Keys {
			fmt.Printf("unseal_key_%d: %s\n", i+1, k)
		}
		return "", fmt.Errorf("vault initialized but init data not persisted: %w", err)
	}

	return rootToken, nil
}

// createProvisionerToken creates a KV-only policy and mints a periodic orphan
// token bound to it. Installers and the `vault` CLI use this token (via
// ResolveVaultToken) instead of root: if leaked it can read/write secrets under
// secret/ but cannot touch sys/ or auth/ (i.e. cannot reconfigure Vault, create
// auth methods, or generate a root token). It is periodic (auto-renews on every
// use) so routine provisioning keeps it alive.
func (v *VaultInstaller) createProvisionerToken(rootToken string) (string, error) {
	policy := `path "secret/data/*" { capabilities = ["create", "read", "update", "delete", "list"] }
path "secret/metadata/*" { capabilities = ["read", "list", "delete"] }`
	if _, err := v.vaultPut("/v1/sys/policies/acl/k8s-provisioner-rw", rootToken, map[string]interface{}{
		"policy": policy,
	}); err != nil {
		return "", fmt.Errorf("create provisioner policy: %w", err)
	}

	resp, err := v.vaultPost("/v1/auth/token/create", rootToken, map[string]interface{}{
		"policies":     []string{"k8s-provisioner-rw"},
		"period":       "8760h",
		"no_parent":    true,
		"display_name": "k8s-provisioner",
	})
	if err != nil {
		return "", fmt.Errorf("create provisioner token: %w", err)
	}

	auth, ok := resp["auth"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("token create: missing auth in response")
	}
	tok, _ := auth["client_token"].(string)
	if tok == "" {
		return "", fmt.Errorf("token create: empty client_token in response")
	}
	return tok, nil
}

// sshConn builds the SSH/SCP auth pieces for reaching another node, honoring
// provisioning config. Prefers key-based auth; falls back to a password (default:
// the Vagrant box credential). Uses StrictHostKeyChecking=accept-new (trust on
// first use, reject key changes) rather than disabling host-key checking entirely.
// usesPassword reports whether sshpass is needed (so the caller can install it).
func (v *VaultInstaller) sshConn() (env, opts, user string, usesPassword bool) {
	p := v.config.Provisioning
	user = v.config.SSHUser()
	opts = "-o StrictHostKeyChecking=accept-new"
	if p.SSHKeyPath != "" {
		return "", opts + " -i " + p.SSHKeyPath, user, false
	}
	pw := p.SSHPassword
	if pw == "" {
		pw = "vagrant"
	}
	return fmt.Sprintf("sshpass -p '%s' ", pw), opts, user, true
}

func (v *VaultInstaller) loadRootToken() (string, error) {
	// Try reading from storage node via SSH
	storageIP := v.config.StorageIP()
	env, opts, user, _ := v.sshConn()
	out, err := v.exec.RunShell(fmt.Sprintf(
		"%sssh %s %s@%s 'sudo cat %s'",
		env, opts, user, storageIP, VaultInitFileRemote,
	))
	if err == nil && out != "" {
		var init vaultInitResponse
		if jsonErr := json.Unmarshal([]byte(out), &init); jsonErr == nil && init.RootToken != "" {
			return init.RootToken, nil
		}
	}

	// Fallback: local controlplane copy
	data, err := os.ReadFile(VaultInitFileLocal)
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
	localPath := VaultInitFileLocal
	if err := os.WriteFile(localPath, raw, 0600); err != nil {
		return fmt.Errorf("failed to save vault-init.json locally: %w", err)
	}

	// Copy to storage node via scp (avoids shell-escaping issues with JSON)
	storageIP := v.config.StorageIP()
	env, opts, user, usesPassword := v.sshConn()
	if usesPassword {
		if _, err := v.exec.RunShell("apt-get install -y sshpass 2>/dev/null || true"); err != nil {
			fmt.Printf("Warning: could not install sshpass: %v\n", err)
		}
	}

	scpCmd := fmt.Sprintf(
		"%sscp %s %s %s@%s:/tmp/vault-init.json",
		env, opts, localPath, user, storageIP,
	)
	moveCmd := fmt.Sprintf(
		"%sssh %s %s@%s 'sudo mv /tmp/vault-init.json %s && sudo chmod 600 %s'",
		env, opts, user, storageIP, VaultInitFileRemote, VaultInitFileRemote,
	)

	if _, err := v.exec.RunShell(scpCmd); err != nil {
		fmt.Printf("Warning: could not scp vault-init.json to storage node: %v\n", err)
		fmt.Printf("Vault init data saved locally at %s\n", localPath)
		return nil
	}
	if _, err := v.exec.RunShell(moveCmd); err != nil {
		fmt.Printf("Warning: could not move vault-init.json on storage node: %v\n", err)
	}

	fmt.Printf("Vault init data saved to %s:%s\n", storageIP, VaultInitFileRemote)
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

	k8sHost := fmt.Sprintf("https://%s:%d", v.config.Network.ControlPlaneIP, apiServerPort)
	if _, err := v.vaultPost("/v1/auth/kubernetes/config", token, map[string]interface{}{
		"kubernetes_host":    k8sHost,
		"kubernetes_ca_cert": string(caCert),
		"token_reviewer_jwt": strings.TrimSpace(saToken),
	}); err != nil {
		return fmt.Errorf("configure k8s auth backend: %w", err)
	}

	// Create policy for k8s workloads — data read + metadata list for VSO version checks
	policy := `path "secret/data/k8s-provisioner/*" { capabilities = ["read", "list"] }
path "secret/metadata/k8s-provisioner/*" { capabilities = ["read", "list"] }`
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

	grafanaPassword, err := v.resolveOrGenerate(token, "grafana_admin_password")
	if err != nil {
		return fmt.Errorf("resolve grafana admin password: %w", err)
	}
	secrets["grafana_admin_password"] = grafanaPassword

	// Keycloak credentials — stored early so VSO can sync them before Keycloak is
	// installed. Usernames are non-secret constants; every password/secret is
	// randomly generated on first run (never a hardcoded default) and persisted
	// here so it survives and is recoverable via `k8s-provisioner vault get`.
	secrets["keycloak_admin_username"] = v.resolveOrDefaultStr(token, "keycloak_admin_username", "admin")
	secrets["keycloak_postgres_username"] = v.resolveOrDefaultStr(token, "keycloak_postgres_username", "keycloak")
	for _, key := range []string{
		"keycloak_admin_password",
		"keycloak_postgres_password",
		"keycloak_grafana_client_secret",
		"keycloak_k8sadmin_password",
		"keycloak_developer_password",
	} {
		val, gerr := v.resolveOrGenerate(token, key)
		if gerr != nil {
			return fmt.Errorf("generate %s: %w", key, gerr)
		}
		secrets[key] = val
	}

	_, err = v.vaultPost("/v1/secret/data/k8s-provisioner/api-keys", token, map[string]interface{}{
		"data": secrets,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Stored %d secret(s) at secret/data/k8s-provisioner/api-keys\n", len(secrets))
	return nil
}

// resolveOrDefaultStr returns the existing Vault value for key, or fallback if absent.
func (v *VaultInstaller) resolveOrDefaultStr(token, key, fallback string) string {
	existing, err := v.vaultGet("/v1/secret/data/k8s-provisioner/api-keys", token)
	if err == nil {
		if data, ok := existing["data"].(map[string]interface{}); ok {
			if inner, ok := data["data"].(map[string]interface{}); ok {
				if val, ok := inner[key].(string); ok && val != "" {
					return val
				}
			}
		}
	}
	return fallback
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

	resp, err := vaultHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Warning: failed to close response body: %v\n", err)
		}
	}()

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

func (v *VaultInstaller) printAccessInfo() {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   HashiCorp Vault configurado com sucesso!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("\nVault UI:  %s/ui\n", v.address)
	fmt.Printf("Vault API: %s\n", v.address)
	fmt.Printf("\nCredenciais salvas em: %s\n", VaultInitFileLocal)
	fmt.Println("\nPara usar o Vault:")
	fmt.Printf("  export VAULT_ADDR=%s\n", v.address)
	fmt.Printf("  export VAULT_TOKEN=$(cat %s | jq -r .root_token)\n", VaultInitFileLocal)
	fmt.Println("\nPara ler todos os secrets:")
	fmt.Println("  vault kv get secret/k8s-provisioner/api-keys")
	fmt.Println("\nSenha do Grafana:")
	fmt.Println("  vault kv get -field=grafana_admin_password secret/k8s-provisioner/api-keys")
	fmt.Println("\nAutenticação Kubernetes (em pods):")
	fmt.Println("  vault write auth/kubernetes/login role=k8s-provisioner jwt=$SA_TOKEN")
	fmt.Println(strings.Repeat("=", 50))
}
