package installer

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// apiKeysPath holds third-party API keys and optional overrides
// (ollama_api_key, karpor_auth_token, alertmanager_config). Logins are not
// stored here: each lives at its own path (see labCredential).
const apiKeysPath = "k8s-provisioner/api-keys"

// labCredential is one login stored at its own KV-v2 path with plain keys —
// {username, password}, or {client_id, client_secret} for an OIDC client — so
// the Vault UI and CLI show a clean login and secret.
type labCredential struct {
	path      string
	loginKey  string
	login     string // fixed login, seeded on first run
	secretKey string
	// Keys of the same values in the flat api-keys secret used before the
	// split; adopted once on upgrade so working passwords survive.
	legacyLoginKey, legacySecretKey string
}

var (
	grafanaAdminCredential = labCredential{path: "k8s-provisioner/grafana/admin",
		loginKey: "username", login: "admin", secretKey: "password",
		legacySecretKey: "grafana_admin_password"}
	keycloakAdminCredential = labCredential{path: "k8s-provisioner/keycloak/admin",
		loginKey: "username", login: "admin", secretKey: "password",
		legacyLoginKey: "keycloak_admin_username", legacySecretKey: "keycloak_admin_password"}
	keycloakPostgresCredential = labCredential{path: "k8s-provisioner/keycloak/postgres",
		loginKey: "username", login: "keycloak", secretKey: "password",
		legacyLoginKey: "keycloak_postgres_username", legacySecretKey: "keycloak_postgres_password"}
	keycloakGrafanaClientCredential = labCredential{path: "k8s-provisioner/keycloak/clients/grafana",
		loginKey: "client_id", login: "grafana", secretKey: "client_secret",
		legacySecretKey: "keycloak_grafana_client_secret"}
	keycloakK8sAdminCredential = labCredential{path: "k8s-provisioner/keycloak/users/k8sadmin",
		loginKey: "username", login: "k8sadmin", secretKey: "password",
		legacySecretKey: "keycloak_k8sadmin_password"}
	keycloakDeveloperCredential = labCredential{path: "k8s-provisioner/keycloak/users/developer",
		loginKey: "username", login: "developer", secretKey: "password",
		legacySecretKey: "keycloak_developer_password"}
)

func labCredentials() []labCredential {
	return []labCredential{
		grafanaAdminCredential,
		keycloakAdminCredential,
		keycloakPostgresCredential,
		keycloakGrafanaClientCredential,
		keycloakK8sAdminCredential,
		keycloakDeveloperCredential,
	}
}

// secretStore is the KV-v2 surface the credential seeding needs (VaultClient in
// production, an in-memory map in tests).
type secretStore interface {
	ReadSecret(path string) (map[string]string, error)
	WriteSecret(path string, data map[string]string) error
}

// ensureCredential returns c's login and secret from the store, writing back
// any missing key: a missing value is adopted from legacy (the pre-split
// api-keys data, may be nil) when present, otherwise the login gets its fixed
// default and the secret is freshly generated.
func ensureCredential(store secretStore, c labCredential, legacy map[string]string) (login, secret string, err error) {
	data, err := store.ReadSecret(c.path)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", c.path, err)
	}
	if data == nil {
		data = map[string]string{}
	}

	changed := false
	if data[c.loginKey] == "" {
		data[c.loginKey] = c.login
		if v := legacy[c.legacyLoginKey]; c.legacyLoginKey != "" && v != "" {
			data[c.loginKey] = v
		}
		changed = true
	}
	if data[c.secretKey] == "" {
		val := legacy[c.legacySecretKey]
		if val == "" {
			if val, err = generatePassword(20); err != nil {
				return "", "", fmt.Errorf("generate %s: %w", c.path, err)
			}
		}
		data[c.secretKey] = val
		changed = true
	}
	if changed {
		if err := store.WriteSecret(c.path, data); err != nil {
			return "", "", fmt.Errorf("write %s: %w", c.path, err)
		}
	}
	return data[c.loginKey], data[c.secretKey], nil
}

// seedLabCredentials makes sure every lab login exists at its own path, then
// drops the pre-split copies from api-keys so each value has one home.
func seedLabCredentials(store secretStore) error {
	legacy, err := store.ReadSecret(apiKeysPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", apiKeysPath, err)
	}
	for _, c := range labCredentials() {
		if _, _, err := ensureCredential(store, c, legacy); err != nil {
			return err
		}
	}

	remaining := make(map[string]string, len(legacy))
	for k, v := range legacy {
		remaining[k] = v
	}
	for _, c := range labCredentials() {
		delete(remaining, c.legacyLoginKey)
		delete(remaining, c.legacySecretKey)
	}
	if len(remaining) == len(legacy) {
		return nil
	}
	return store.WriteSecret(apiKeysPath, remaining)
}

// SecretResolver centralizes the "try Vault → fall back to a default" chain that
// every installer needs. Previously this logic (Vault enablement check, token
// resolution, key lookup, default fallback) was reimplemented per installer with
// subtly different fallback orders. A resolver with no reachable Vault is in
// defaults-only mode and always returns the supplied default.
type SecretResolver struct {
	vault *VaultClient
}

// NewSecretResolver builds a resolver from config. If Vault is not configured or
// the token cannot be resolved, the resolver runs in defaults-only mode.
func NewSecretResolver(cfg *config.Config) *SecretResolver {
	if !cfg.Vault.Enabled {
		return &SecretResolver{}
	}
	token := ResolveVaultToken(cfg.Vault.Token)
	if token == "" {
		return &SecretResolver{}
	}
	return &SecretResolver{vault: NewVaultClient(cfg.VaultAddress(), token, cfg.VaultCACert())}
}

// Enabled reports whether a Vault backend is available.
func (r *SecretResolver) Enabled() bool { return r.vault != nil }

// Client returns the underlying Vault client (nil in defaults-only mode), for
// callers that need read+write access beyond simple resolution (e.g. Keycloak,
// which seeds missing defaults back into Vault).
func (r *SecretResolver) Client() *VaultClient { return r.vault }

// Credential returns c's login and secret as stored in Vault; empty strings in
// defaults-only mode or when the path is missing.
func (r *SecretResolver) Credential(c labCredential) (login, secret string) {
	if r.vault == nil {
		return "", ""
	}
	return r.vault.GetValue(c.path, c.loginKey, ""), r.vault.GetValue(c.path, c.secretKey, "")
}

// Resolve returns the first non-empty value among keys at the api-keys path,
// falling back to def. In defaults-only mode it returns def immediately. When a
// value is found in Vault, label (if non-empty) is logged.
func (r *SecretResolver) Resolve(label, def string, keys ...string) string {
	if r.vault == nil {
		return def
	}
	for _, key := range keys {
		if val := r.vault.GetValue(apiKeysPath, key, ""); val != "" {
			if label != "" {
				fmt.Printf("%s loaded from Vault\n", label)
			}
			return val
		}
	}
	return def
}
