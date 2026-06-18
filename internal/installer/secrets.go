package installer

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// apiKeysPath is the KV-v2 path under which all component secrets are stored.
const apiKeysPath = "k8s-provisioner/api-keys"

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
	return &SecretResolver{vault: NewVaultClient(cfg.VaultAddress(), token)}
}

// Enabled reports whether a Vault backend is available.
func (r *SecretResolver) Enabled() bool { return r.vault != nil }

// Client returns the underlying Vault client (nil in defaults-only mode), for
// callers that need read+write access beyond simple resolution (e.g. Keycloak,
// which seeds missing defaults back into Vault).
func (r *SecretResolver) Client() *VaultClient { return r.vault }

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
