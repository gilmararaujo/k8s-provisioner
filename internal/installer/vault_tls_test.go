package installer

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// tlsVaultServer starts an HTTPS test server and writes its self-signed
// certificate to a PEM file, returning the server and the CA path.
func tlsVaultServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	caPath := filepath.Join(t.TempDir(), "vault-ca.crt")
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	require.NoError(t, os.WriteFile(caPath, caPEM, 0o600))
	return srv, caPath
}

func TestNewVaultHTTPClient_TrustsCAFile(t *testing.T) {
	srv, caPath := tlsVaultServer(t)

	resp, err := newVaultHTTPClient(caPath).Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNewVaultHTTPClient_RejectsUnknownCA(t *testing.T) {
	srv, _ := tlsVaultServer(t)

	// Missing CA file → system roots only, which do not include the test cert.
	_, err := newVaultHTTPClient(filepath.Join(t.TempDir(), "missing.crt")).Get(srv.URL)
	assert.Error(t, err, "a Vault cert from an untrusted CA must be rejected")
}

func vaultTLSConfig(caPath string) *config.Config {
	cfg := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
	cfg.Vault.Enabled = true
	cfg.Vault.CACert = caPath
	return cfg
}

func TestEnsureVaultCA_FetchesFromStorageNode(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "sub", "vault-ca.crt")
	sh := &fakeShell{outputs: map[string]string{
		"cat " + vaultCAFileRemote: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
	}}

	require.NoError(t, ensureVaultCA(vaultTLSConfig(caPath), sh))

	got, err := os.ReadFile(caPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "BEGIN CERTIFICATE")
}

func TestEnsureVaultCA_KeepsExistingFile(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "vault-ca.crt")
	require.NoError(t, os.WriteFile(caPath, []byte("operator-supplied"), 0o600))
	sh := &fakeShell{}

	require.NoError(t, ensureVaultCA(vaultTLSConfig(caPath), sh))
	assert.Empty(t, sh.calls, "no SSH fetch when the CA is already present")
}

func TestEnsureVaultCA_SkipsPlainHTTPVault(t *testing.T) {
	cfg := vaultTLSConfig(filepath.Join(t.TempDir(), "vault-ca.crt"))
	cfg.Vault.Addr = "http://vault.example:8200"
	sh := &fakeShell{}

	require.NoError(t, ensureVaultCA(cfg, sh))
	assert.Empty(t, sh.calls)
}

func TestEnsureVaultCA_RejectsNonPEM(t *testing.T) {
	sh := &fakeShell{outputs: map[string]string{"cat " + vaultCAFileRemote: "cat: No such file"}}

	err := ensureVaultCA(vaultTLSConfig(filepath.Join(t.TempDir(), "vault-ca.crt")), sh)
	assert.ErrorContains(t, err, "not a PEM certificate")
}
