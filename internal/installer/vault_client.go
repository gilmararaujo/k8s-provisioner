package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const vaultMount = "secret"

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