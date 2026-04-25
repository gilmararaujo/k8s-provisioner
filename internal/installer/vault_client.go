package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const vaultMount = "secret"

// vaultInitFile is written by the provisioner during Vault initialization.
const vaultInitFile = "/etc/k8s-provisioner/vault-init.json"

type vaultInitData struct {
	RootToken string `json:"root_token"`
}

// ResolveVaultToken returns the token from config, falling back to vault-init.json.
func ResolveVaultToken(configToken string) string {
	if configToken != "" {
		return configToken
	}

	data, err := os.ReadFile(vaultInitFile)
	if err != nil {
		return ""
	}

	var init vaultInitData
	if err := json.Unmarshal(data, &init); err != nil {
		return ""
	}

	return init.RootToken
}

type VaultClient struct {
	addr   string
	token  string
	client *http.Client
}

func NewVaultClient(addr, token string) *VaultClient {
	return &VaultClient{
		addr:  addr,
		token: token,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ReadSecret reads all key-value pairs from a KV v2 path.
// Returns nil, nil if the secret does not exist yet.
func (v *VaultClient) ReadSecret(path string) (map[string]string, error) {
	url := fmt.Sprintf("%s/v1/%s/data/%s", v.addr, vaultMount, path)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Vault-Token", v.token)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vault unreachable at %s: %w", v.addr, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			fmt.Printf("Warning: failed to close vault response body: %v\n", cerr)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vault read %s: HTTP %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return result.Data.Data, nil
}

// WriteSecret writes key-value pairs to a KV v2 path.
// Creates or updates (new version) the secret.
func (v *VaultClient) WriteSecret(path string, data map[string]string) error {
	url := fmt.Sprintf("%s/v1/%s/data/%s", v.addr, vaultMount, path)

	payload := map[string]interface{}{"data": data}
	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", v.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("vault unreachable at %s: %w", v.addr, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			fmt.Printf("Warning: failed to close vault response body: %v\n", cerr)
		}
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("vault write %s: HTTP %d", path, resp.StatusCode)
	}

	return nil
}

// GetValue reads a single key from a KV v2 path.
// Falls back to defaultVal if Vault is unavailable or key is missing.
func (v *VaultClient) GetValue(path, key, defaultVal string) string {
	data, err := v.ReadSecret(path)
	if err != nil {
		fmt.Printf("Warning: could not read Vault secret %s: %v — using default\n", path, err)
		return defaultVal
	}
	if data == nil {
		return defaultVal
	}
	if val, ok := data[key]; ok && val != "" {
		return val
	}
	return defaultVal
}

// UnsealWithKey sends a single unseal key to Vault's /v1/sys/unseal endpoint.
func UnsealWithKey(addr, key string) error {
	url := fmt.Sprintf("%s/v1/sys/unseal", addr)
	payload := map[string]string{"key": key}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body)) //nolint:noctx,G107
	if err != nil {
		return fmt.Errorf("vault unreachable at %s: %w", addr, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vault unseal: HTTP %d", resp.StatusCode)
	}
	return nil
}

// FetchSecret reads a single key from the k8s-provisioner/api-keys KV path.
// addr and token are vault address and token respectively.
// Returns ("", err) if Vault is not configured or the key is missing.
func FetchSecret(addr, token, key string) (string, error) {
	if addr == "" {
		return "", fmt.Errorf("vault not configured")
	}
	token = ResolveVaultToken(token)
	if token == "" {
		return "", fmt.Errorf("vault token not available")
	}
	v := NewVaultClient(addr, token)
	data, err := v.ReadSecret("k8s-provisioner/api-keys")
	if err != nil {
		return "", err
	}
	if data == nil {
		return "", fmt.Errorf("secret path not found")
	}
	val, ok := data[key]
	if !ok || val == "" {
		return "", fmt.Errorf("key %q not found", key)
	}
	return val, nil
}