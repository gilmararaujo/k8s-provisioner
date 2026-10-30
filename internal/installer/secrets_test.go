package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestSecretResolver_DefaultsOnlyWhenVaultDisabled(t *testing.T) {
	r := NewSecretResolver(&config.Config{}) // Vault.Enabled == false

	assert.False(t, r.Enabled(), "resolver must be in defaults-only mode")
	assert.Equal(t, "fallback", r.Resolve("", "fallback", "some_key"),
		"with no Vault backend Resolve returns the default")
}

// memStore is an in-memory secretStore that records every write.
type memStore struct {
	data   map[string]map[string]string
	writes []string
}

func (m *memStore) ReadSecret(path string) (map[string]string, error) {
	if m.data[path] == nil {
		return nil, nil // like Vault: a missing path is not an error
	}
	out := map[string]string{}
	for k, v := range m.data[path] {
		out[k] = v
	}
	return out, nil
}

func (m *memStore) WriteSecret(path string, data map[string]string) error {
	if m.data == nil {
		m.data = map[string]map[string]string{}
	}
	m.data[path] = data
	m.writes = append(m.writes, path)
	return nil
}

func TestEnsureCredential_SeedsCleanLoginAndPassword(t *testing.T) {
	store := &memStore{}

	login, secret, err := ensureCredential(store, keycloakK8sAdminCredential, nil)

	require.NoError(t, err)
	assert.Equal(t, "k8sadmin", login)
	assert.Len(t, secret, 20)
	assert.Equal(t, map[string]string{"username": "k8sadmin", "password": secret},
		store.data["k8s-provisioner/keycloak/users/k8sadmin"], "only the plain login and password keys")
}

func TestEnsureCredential_KeepsExistingValues(t *testing.T) {
	store := &memStore{data: map[string]map[string]string{
		"k8s-provisioner/keycloak/admin": {"username": "root", "password": "kept"},
	}}

	login, secret, err := ensureCredential(store, keycloakAdminCredential, nil)

	require.NoError(t, err)
	assert.Equal(t, "root", login)
	assert.Equal(t, "kept", secret)
	assert.Empty(t, store.writes, "nothing missing, nothing written")
}

func TestSeedLabCredentials_MigratesFlatApiKeys(t *testing.T) {
	store := &memStore{data: map[string]map[string]string{apiKeysPath: {
		"grafana_admin_password":         "gpw",
		"keycloak_admin_username":        "kcadmin",
		"keycloak_admin_password":        "kpw",
		"keycloak_grafana_client_secret": "cs",
		"keycloak_k8sadmin_password":     "F0875",
		"ollama_api_key":                 "olka",
		"alertmanager_config":            "route: {}",
	}}}

	require.NoError(t, seedLabCredentials(store))

	assert.Equal(t, map[string]string{"username": "admin", "password": "gpw"}, store.data["k8s-provisioner/grafana/admin"])
	assert.Equal(t, map[string]string{"username": "kcadmin", "password": "kpw"}, store.data["k8s-provisioner/keycloak/admin"])
	assert.Equal(t, map[string]string{"client_id": "grafana", "client_secret": "cs"}, store.data["k8s-provisioner/keycloak/clients/grafana"])
	assert.Equal(t, "F0875", store.data["k8s-provisioner/keycloak/users/k8sadmin"]["password"], "upgrades keep working passwords")
	assert.NotEmpty(t, store.data["k8s-provisioner/keycloak/users/developer"]["password"], "absent before: generated")
	assert.Equal(t, map[string]string{"ollama_api_key": "olka", "alertmanager_config": "route: {}"}, store.data[apiKeysPath],
		"api-keys keeps only API keys and overrides")
}
