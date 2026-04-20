package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Manage HashiCorp Vault on the storage node",
}

var vaultStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Vault status",
	RunE: func(cmd *cobra.Command, args []string) error {
		addr := vaultAddress()
		return printVaultStatus(addr)
	},
}

var vaultInitInfoCmd = &cobra.Command{
	Use:   "init-info",
	Short: "Show how to retrieve Vault init credentials from the storage node",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("As credenciais do Vault ficam no storage node.")
		fmt.Println("Para acessá-las, execute:")
		fmt.Println()
		fmt.Println("  vagrant ssh storage -c 'sudo cat /etc/vault.d/vault-init.json'")
		fmt.Println()
		fmt.Println("O campo 'root_token' é a senha para acessar a UI em:")
		fmt.Printf("  %s/ui\n", vaultAddress())
	},
}

var vaultGetSecretCmd = &cobra.Command{
	Use:   "get-secret <path>",
	Short: "Read a secret from Vault (requires VAULT_TOKEN env var)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		token := os.Getenv("VAULT_TOKEN")
		if token == "" {
			return fmt.Errorf("VAULT_TOKEN environment variable is required")
		}
		addr := vaultAddress()
		return getVaultSecret(addr, token, args[0])
	},
}

func vaultAddress() string {
	if cfg != nil && cfg.Vault.Address != "" {
		return cfg.Vault.Address
	}
	return "http://192.168.56.20:8200"
}

func printVaultStatus(addr string) error {
	resp, err := http.Get(addr + "/v1/sys/health")
	if err != nil {
		return fmt.Errorf("cannot reach Vault at %s: %w", addr, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Warning: failed to close response body: %v\n", err)
		}
	}()

	var health map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return err
	}

	fmt.Printf("Vault Address:   %s\n", addr)
	fmt.Printf("Initialized:     %v\n", health["initialized"])
	fmt.Printf("Sealed:          %v\n", health["sealed"])
	fmt.Printf("Version:         %v\n", health["version"])

	switch resp.StatusCode {
	case 200:
		fmt.Println("Status:          active")
	case 429:
		fmt.Println("Status:          standby")
	case 501:
		fmt.Println("Status:          not initialized")
	case 503:
		fmt.Println("Status:          sealed")
	default:
		fmt.Printf("HTTP Status:     %d\n", resp.StatusCode)
	}
	return nil
}

func getVaultSecret(addr, token, secretPath string) error {
	// KV v2 path: secret/data/<path>
	kvPath := "/v1/secret/data/" + secretPath

	req, err := http.NewRequest("GET", addr+kvPath, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			fmt.Printf("Warning: failed to close response body: %v\n", err)
		}
	}()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("vault returned %d: %v", resp.StatusCode, result)
	}

	raw, _ := json.MarshalIndent(result["data"], "", "  ")
	fmt.Println(string(raw))
	return nil
}

func init() {
	vaultCmd.AddCommand(vaultStatusCmd)
	vaultCmd.AddCommand(vaultInitInfoCmd)
	vaultCmd.AddCommand(vaultGetSecretCmd)
	rootCmd.AddCommand(vaultCmd)
}
