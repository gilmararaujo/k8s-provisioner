package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/techiescamp/k8s-provisioner/internal/installer"
)

var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Interact with HashiCorp Vault",
	Long: `Read and write secrets in HashiCorp Vault running on the storage node.

Vault stores all cluster secrets at path k8s-provisioner/api-keys:
  - grafana_admin_password
  - keycloak_admin_password
  - keycloak_postgres_password
  - grafana_oidc_client_secret

The Vault token is auto-resolved from /etc/k8s-provisioner/vault-init.json
if not set in config.yaml.`,
}

var vaultTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print the Vault root token",
	Long: `Print the root token from /etc/k8s-provisioner/vault-init.json.

This file is written by k8s-provisioner during cluster setup.
Run this on the controlplane node or any host where the file exists.`,
	RunE: runVaultToken,
}

var vaultStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Vault connectivity and seal status",
	RunE:  runVaultStatus,
}

var vaultGetCmd = &cobra.Command{
	Use:   "get <path> [key]",
	Short: "Read a secret from Vault",
	Long: `Read secrets stored in Vault KV v2.

Examples:
  # Read all keys at a path
  k8s-provisioner vault get k8s-provisioner/api-keys

  # Read a specific key
  k8s-provisioner vault get k8s-provisioner/api-keys grafana_admin_password`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runVaultGet,
}

var vaultSetCmd = &cobra.Command{
	Use:   "set <path> <key> <value>",
	Short: "Write a secret to Vault",
	Long: `Write a key-value pair to Vault KV v2. Existing keys at the path are preserved.

Example:
  k8s-provisioner vault set k8s-provisioner/api-keys grafana_admin_password MyNewPass123`,
	Args: cobra.ExactArgs(3),
	RunE: runVaultSet,
}

var vaultUnsealCmd = &cobra.Command{
	Use:   "unseal",
	Short: "Unseal Vault using keys from vault-init.json",
	Long: `Unseal Vault after a storage node reboot.

Reads unseal keys from /etc/k8s-provisioner/vault-init.json and sends
the first 3 keys (threshold) to the Vault API on the storage node.`,
	RunE: runVaultUnseal,
}

func init() {
	rootCmd.AddCommand(vaultCmd)
	vaultCmd.AddCommand(vaultTokenCmd)
	vaultCmd.AddCommand(vaultStatusCmd)
	vaultCmd.AddCommand(vaultGetCmd)
	vaultCmd.AddCommand(vaultSetCmd)
	vaultCmd.AddCommand(vaultUnsealCmd)

	// vault subcommands need config
	noConfigCommands["vault"] = false
}

func vaultClientFromConfig() (*installer.VaultClient, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config not loaded")
	}
	addr := cfg.Vault.Addr
	if addr == "" {
		addr = "http://192.168.56.20:8200"
	}
	token := installer.ResolveVaultToken(cfg.Vault.Token)
	if token == "" {
		return nil, fmt.Errorf("vault token not found — run 'k8s-provisioner vault token' on the controlplane or set vault.token in config.yaml")
	}
	return installer.NewVaultClient(addr, token), nil
}

func runVaultToken(_ *cobra.Command, _ []string) error {
	const initFile = "/etc/k8s-provisioner/vault-init.json"

	data, err := os.ReadFile(initFile)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w\n\nRun this command on the controlplane node after provisioning", initFile, err)
	}

	var init struct {
		RootToken string `json:"root_token"`
		Keys      []string `json:"keys"`
	}
	if err := json.Unmarshal(data, &init); err != nil {
		return fmt.Errorf("failed to parse %s: %w", initFile, err)
	}
	if init.RootToken == "" {
		return fmt.Errorf("root_token is empty in %s", initFile)
	}

	fmt.Println(init.RootToken)
	return nil
}

func runVaultStatus(_ *cobra.Command, _ []string) error {
	client, err := vaultClientFromConfig()
	if err != nil {
		return err
	}

	// Try to read a known path to verify connectivity + token validity
	_, err = client.ReadSecret("k8s-provisioner/api-keys")
	if err != nil {
		return fmt.Errorf("vault unreachable or token invalid: %w", err)
	}

	addr := cfg.Vault.Addr
	if addr == "" {
		addr = "http://192.168.56.20:8200"
	}
	fmt.Printf("Vault is reachable at %s\n", addr)
	fmt.Println("Token: valid")
	return nil
}

func runVaultGet(_ *cobra.Command, args []string) error {
	client, err := vaultClientFromConfig()
	if err != nil {
		return err
	}

	path := args[0]
	data, err := client.ReadSecret(path)
	if err != nil {
		return err
	}
	if data == nil {
		fmt.Printf("No secret found at path: %s\n", path)
		return nil
	}

	if len(args) == 2 {
		key := args[1]
		val, ok := data[key]
		if !ok {
			return fmt.Errorf("key %q not found at path %s", key, path)
		}
		fmt.Println(val)
		return nil
	}

	// Print all keys
	fmt.Printf("Secrets at %s:\n", path)
	for k, v := range data {
		fmt.Printf("  %-40s = %s\n", k, v)
	}
	return nil
}

func runVaultSet(_ *cobra.Command, args []string) error {
	client, err := vaultClientFromConfig()
	if err != nil {
		return err
	}

	path, key, value := args[0], args[1], args[2]

	// Read existing to preserve other keys
	existing, err := client.ReadSecret(path)
	if err != nil {
		return err
	}
	if existing == nil {
		existing = make(map[string]string)
	}

	existing[key] = value

	if err := client.WriteSecret(path, existing); err != nil {
		return err
	}

	fmt.Printf("Set %s at %s\n", key, path)
	return nil
}

func runVaultUnseal(_ *cobra.Command, _ []string) error {
	const initFile = "/etc/k8s-provisioner/vault-init.json"

	data, err := os.ReadFile(initFile)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", initFile, err)
	}

	var init struct {
		Keys      []string `json:"keys"`
		RootToken string   `json:"root_token"`
	}
	if err := json.Unmarshal(data, &init); err != nil {
		return fmt.Errorf("failed to parse %s: %w", initFile, err)
	}
	if len(init.Keys) == 0 {
		return fmt.Errorf("no unseal keys found in %s", initFile)
	}

	addr := "http://192.168.56.20:8200"
	if cfg != nil && cfg.Vault.Addr != "" {
		addr = cfg.Vault.Addr
	}

	threshold := 3
	if len(init.Keys) < threshold {
		threshold = len(init.Keys)
	}

	fmt.Printf("Unsealing Vault at %s (sending %d of %d keys)...\n", addr, threshold, len(init.Keys))

	for i := 0; i < threshold; i++ {
		if err := installer.UnsealWithKey(addr, init.Keys[i]); err != nil {
			return fmt.Errorf("unseal key %d failed: %w", i+1, err)
		}
		fmt.Printf("  Key %d accepted\n", i+1)
	}

	fmt.Println("Vault unsealed successfully!")
	return nil
}