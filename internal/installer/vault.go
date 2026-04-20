package installer

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

const vaultInitFilePath = "/etc/k8s-provisioner/vault-init.json"

type VaultInstaller struct {
	config *config.Config
	exec   executor.CommandExecutor
}

type vaultInitOutput struct {
	Keys      []string `json:"keys"`
	RootToken string   `json:"root_token"`
}

func NewVaultInstaller(cfg *config.Config, exec executor.CommandExecutor) *VaultInstaller {
	return &VaultInstaller{config: cfg, exec: exec}
}

func (v *VaultInstaller) Install() error {
	storageHost := v.config.Storage.NFSServer
	if storageHost == "" {
		storageHost = "storage"
	}

	fmt.Printf("Installing Vault on %s node...\n", storageHost)
	if err := v.installVault(storageHost); err != nil {
		return err
	}

	fmt.Println("Configuring Vault...")
	if err := v.writeConfig(storageHost); err != nil {
		return err
	}

	fmt.Println("Starting Vault service...")
	if err := v.startService(storageHost); err != nil {
		return err
	}

	fmt.Println("Waiting for Vault API...")
	if err := v.waitForAPI(storageHost); err != nil {
		return err
	}

	fmt.Println("Initializing Vault...")
	initData, err := v.initOrLoad(storageHost)
	if err != nil {
		return err
	}

	fmt.Println("Unsealing Vault...")
	if err := v.unsealVault(storageHost, initData); err != nil {
		return err
	}

	fmt.Println("Saving init data...")
	if err := v.saveInitFiles(storageHost, initData); err != nil {
		return err
	}

	fmt.Println("Enabling KV secrets engine...")
	if err := v.enableKVEngine(storageHost, initData.RootToken); err != nil {
		fmt.Printf("Warning: KV engine setup failed (may already exist): %v\n", err)
	}

	fmt.Println("Vault installed successfully!")
	v.printAccessInfo(storageHost, initData)
	return nil
}

func (v *VaultInstaller) installVault(host string) error {
	// Install sshpass if needed
	if _, err := v.exec.RunShell("which sshpass 2>/dev/null || apt-get install -y sshpass"); err != nil {
		return err
	}

	script := `
set -e
if command -v vault &>/dev/null; then
  echo "Vault already installed: $(vault version)"
  exit 0
fi
apt-get install -y wget gpg lsb-release
wget -O- https://apt.releases.hashicorp.com/gpg | gpg --dearmor -o /usr/share/keyrings/hashicorp-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/hashicorp-archive-keyring.gpg] https://apt.releases.hashicorp.com $(lsb_release -cs) main" \
  > /etc/apt/sources.list.d/hashicorp.list
apt-get update -y
apt-get install -y vault
echo "Vault installed: $(vault version)"
`
	if err := executor.WriteFile("/tmp/install-vault.sh", script); err != nil {
		return err
	}

	if _, err := v.scp(host, "/tmp/install-vault.sh", "/tmp/install-vault.sh"); err != nil {
		return err
	}

	_, err := v.ssh(host, "bash /tmp/install-vault.sh")
	return err
}

func (v *VaultInstaller) writeConfig(host string) error {
	storageIP := "192.168.56.20"
	// Resolve actual IP from nodes if available
	for _, node := range v.config.Nodes {
		if node.Role == "storage" && node.IP != "" {
			storageIP = node.IP
		}
	}

	cfg := fmt.Sprintf(`storage "file" {
  path = "/opt/vault/data"
}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = "true"
}

api_addr = "http://%s:8200"
ui = true
`, storageIP)

	if err := executor.WriteFile("/tmp/vault.hcl", cfg); err != nil {
		return err
	}

	if _, err := v.scp(host, "/tmp/vault.hcl", "/tmp/vault.hcl"); err != nil {
		return err
	}

	_, err := v.ssh(host, "mkdir -p /opt/vault/data && mv /tmp/vault.hcl /etc/vault.d/vault.hcl && chown -R vault:vault /etc/vault.d /opt/vault/data")
	return err
}

func (v *VaultInstaller) startService(host string) error {
	if _, err := v.ssh(host, "systemctl enable vault && systemctl restart vault"); err != nil {
		return err
	}
	return nil
}

func (v *VaultInstaller) waitForAPI(host string) error {
	deadline := time.Now().Add(ShortReadyTimeout)
	for time.Now().Before(deadline) {
		out, _ := v.ssh(host, "VAULT_ADDR=http://localhost:8200 vault status -format=json 2>/dev/null | grep -c 'initialized' || echo 0")
		if strings.TrimSpace(out) != "0" {
			fmt.Println("Vault API is up!")
			return nil
		}
		fmt.Println("Waiting for Vault API...")
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for Vault API")
}

func (v *VaultInstaller) initOrLoad(host string) (*vaultInitOutput, error) {
	// Check if already initialized
	out, _ := v.ssh(host, "VAULT_ADDR=http://localhost:8200 vault status 2>/dev/null | grep 'Initialized' | awk '{print $2}'")
	if strings.TrimSpace(out) == "true" {
		fmt.Println("Vault already initialized, loading existing init data...")
		return v.loadExistingInitData(host)
	}

	// Initialize: 5 shares, threshold 3
	raw, err := v.ssh(host, "VAULT_ADDR=http://localhost:8200 vault operator init -key-shares=5 -key-threshold=3 -format=json")
	if err != nil {
		return nil, fmt.Errorf("vault operator init failed: %w", err)
	}

	// vault output may have extra lines before the JSON
	jsonStart := strings.Index(raw, "{")
	if jsonStart < 0 {
		return nil, fmt.Errorf("no JSON in vault init output: %s", raw)
	}
	raw = raw[jsonStart:]

	// The full init output has unseal_keys_hex; map to our simplified format
	var full struct {
		UnsealKeysHex []string `json:"unseal_keys_hex"`
		RootToken     string   `json:"root_token"`
	}
	if err := json.Unmarshal([]byte(raw), &full); err != nil {
		return nil, fmt.Errorf("failed to parse vault init JSON: %w", err)
	}

	return &vaultInitOutput{
		Keys:      full.UnsealKeysHex,
		RootToken: full.RootToken,
	}, nil
}

func (v *VaultInstaller) loadExistingInitData(host string) (*vaultInitOutput, error) {
	// Try controlplane file first (already copied in a previous run)
	if data, err := executor.ReadFileContents(vaultInitFilePath); err == nil {
		var init vaultInitOutput
		if err := json.Unmarshal([]byte(data), &init); err == nil && init.RootToken != "" {
			return &init, nil
		}
	}

	// Fall back to storage node
	raw, err := v.ssh(host, fmt.Sprintf("cat %s", vaultInitFilePath))
	if err != nil {
		return nil, fmt.Errorf("vault already initialized but init file not found on %s or controlplane", host)
	}

	var init vaultInitOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &init); err != nil {
		return nil, err
	}
	return &init, nil
}

func (v *VaultInstaller) unsealVault(host string, data *vaultInitOutput) error {
	// Need 3 of 5 keys (threshold)
	for i := 0; i < 3 && i < len(data.Keys); i++ {
		cmd := fmt.Sprintf("VAULT_ADDR=http://localhost:8200 vault operator unseal %s", data.Keys[i])
		if _, err := v.ssh(host, cmd); err != nil {
			return fmt.Errorf("unseal with key %d failed: %w", i, err)
		}
	}

	// Verify unsealed
	out, _ := v.ssh(host, "VAULT_ADDR=http://localhost:8200 vault status 2>/dev/null | grep 'Sealed' | awk '{print $2}'")
	if strings.TrimSpace(out) == "true" {
		return fmt.Errorf("vault is still sealed after unseal attempts")
	}

	fmt.Println("Vault unsealed successfully!")
	return nil
}

func (v *VaultInstaller) saveInitFiles(host string, data *vaultInitOutput) error {
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	jsonStr := string(jsonBytes)

	// Save on storage node
	saveCmd := fmt.Sprintf("mkdir -p /etc/k8s-provisioner && cat > %s <<'VAULTEOF'\n%s\nVAULTEOF", vaultInitFilePath, jsonStr)
	if _, err := v.ssh(host, saveCmd); err != nil {
		fmt.Printf("Warning: could not save init file on storage node: %v\n", err)
	}

	// Save on controlplane (local)
	if err := executor.WriteFile(vaultInitFilePath, jsonStr); err != nil {
		return fmt.Errorf("failed to save init file on controlplane: %w", err)
	}

	fmt.Printf("Init data saved to %s\n", vaultInitFilePath)
	return nil
}

func (v *VaultInstaller) enableKVEngine(host, rootToken string) error {
	// Check if already enabled
	out, _ := v.ssh(host, fmt.Sprintf("VAULT_ADDR=http://localhost:8200 VAULT_TOKEN=%s vault secrets list 2>/dev/null | grep -c 'secret/' || echo 0", rootToken))
	if strings.TrimSpace(out) != "0" {
		fmt.Println("KV engine already enabled at secret/")
		return nil
	}

	cmd := fmt.Sprintf("VAULT_ADDR=http://localhost:8200 VAULT_TOKEN=%s vault secrets enable -path=secret kv-v2", rootToken)
	_, err := v.ssh(host, cmd)
	return err
}

// ssh runs a command on the storage node as root via vagrant SSH.
func (v *VaultInstaller) ssh(host, cmd string) (string, error) {
	fullCmd := fmt.Sprintf("sshpass -p 'vagrant' ssh -o StrictHostKeyChecking=no -o ConnectTimeout=10 vagrant@%s 'sudo %s'",
		host, strings.ReplaceAll(cmd, "'", `'"'"'`))
	return v.exec.RunShell(fullCmd)
}

// scp copies a local file to the storage node.
func (v *VaultInstaller) scp(host, src, dst string) (string, error) {
	cmd := fmt.Sprintf("sshpass -p 'vagrant' scp -o StrictHostKeyChecking=no %s vagrant@%s:%s", src, host, dst)
	return v.exec.RunShell(cmd)
}

func (v *VaultInstaller) printAccessInfo(host string, data *vaultInitOutput) {
	fmt.Println("\n========================================")
	fmt.Println("Vault Access Information")
	fmt.Println("========================================")
	fmt.Printf("  UI:          http://%s:8200\n", host)
	fmt.Printf("  Root Token:  %s\n", data.RootToken)
	fmt.Printf("  Init File:   %s\n", vaultInitFilePath)
	fmt.Println("\nUnseal keys (keep safe!):")
	for i, key := range data.Keys {
		fmt.Printf("  Key %d: %s\n", i+1, key)
	}
	fmt.Println("\nTo access Vault from the host:")
	fmt.Printf("  export VAULT_ADDR=http://192.168.56.20:8200\n")
	fmt.Printf("  export VAULT_TOKEN=%s\n", data.RootToken)
	fmt.Println("========================================")
}