package installer

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

// vaultCAFileRemote is the Vault CA generated on the storage node by the
// Vagrant provisioning (see vagrant/Vagrantfile), next to Vault's server cert.
const vaultCAFileRemote = "/etc/vault.d/tls/ca.crt"

// vaultRequestTimeout bounds every Vault HTTP call so a hung Vault on a
// freshly-booted storage node fails fast instead of blocking the deadline
// loops indefinitely (RES-1).
const vaultRequestTimeout = 10 * time.Second

// newVaultHTTPClient returns an HTTP client for Vault that trusts the system
// roots plus the CA bundle at caPath. A missing file is not an error: the system
// roots alone cover a Vault with a publicly trusted cert, or a host where the
// lab Vault CA was added to the OS trust store.
func newVaultHTTPClient(caPath string) *http.Client {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if pem, err := os.ReadFile(caPath); err == nil { // #nosec G304 -- operator-supplied CA path
		if !pool.AppendCertsFromPEM(pem) {
			fmt.Printf("Warning: no valid PEM certificates in Vault CA file %s\n", caPath)
		}
	}
	return &http.Client{
		Timeout: vaultRequestTimeout,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}
}

// runOnStorage runs cmd on the storage node over SSH (installing sshpass first
// when password auth is used) and returns its output.
func runOnStorage(cfg *config.Config, exec executor.ShellExecutor, cmd string) (string, error) {
	env, opts, user, usesPassword := sshConnFor(cfg)
	if usesPassword {
		if _, err := exec.RunShell("command -v sshpass >/dev/null || apt-get install -y sshpass"); err != nil {
			return "", fmt.Errorf("install sshpass: %w", err)
		}
	}
	return exec.RunShell(fmt.Sprintf("%sssh %s %s@%s '%s'", env, opts, user, cfg.StorageIP(), cmd))
}

// ensureVaultCA makes the Vault CA available locally at cfg.VaultCACert() so
// every Vault client on this node can verify Vault's certificate. When the file
// is missing it is fetched from the storage node over SSH (the same channel used
// for vault-init.json). Nothing to do for a plain-HTTP Vault or when the file is
// already present (e.g. supplied by the operator via vault.ca_cert).
func ensureVaultCA(cfg *config.Config, exec executor.ShellExecutor) error {
	caPath := cfg.VaultCACert()
	if !strings.HasPrefix(cfg.VaultAddress(), "https://") {
		return nil
	}
	if _, err := os.Stat(caPath); err == nil {
		return nil
	}
	storageIP := cfg.StorageIP()
	if storageIP == "" {
		return fmt.Errorf("vault CA %s not found and no storage node to fetch it from", caPath)
	}

	pem, err := runOnStorage(cfg, exec, "cat "+vaultCAFileRemote)
	if err != nil {
		return fmt.Errorf("fetch vault CA from %s:%s: %w", storageIP, vaultCAFileRemote, err)
	}
	if !strings.Contains(pem, "BEGIN CERTIFICATE") {
		return fmt.Errorf("fetch vault CA from %s:%s: not a PEM certificate", storageIP, vaultCAFileRemote)
	}
	if err := os.MkdirAll(filepath.Dir(caPath), 0o755); err != nil { // #nosec G301 -- holds a public CA cert
		return fmt.Errorf("create %s: %w", filepath.Dir(caPath), err)
	}
	if err := executor.WriteFile(caPath, strings.TrimSpace(pem)+"\n"); err != nil {
		return fmt.Errorf("write vault CA to %s: %w", caPath, err)
	}
	fmt.Printf("Vault CA fetched from storage node → %s\n", caPath)
	return nil
}
