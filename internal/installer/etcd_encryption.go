package installer

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

// etcd encryption at rest, backed by Vault's Transit engine through the
// vault-kubernetes-kms plugin (KMS v2).
//
// The data encryption key never leaves the API server's memory unencrypted; it
// is sealed by a key encryption key that lives only in Vault. A stolen etcd
// snapshot is therefore useless without Vault. The KMS plugin runs as a host
// systemd service on the control plane (not a static pod) because its arm64
// build ships only as a binary, and a host service is up before the API server,
// which removes the image pull and the pod race from the bootstrap path.
//
// The Vault side (Transit key, policy, AppRole) and the plugin credentials in
// kmsEnvFile are produced by the Vault installer; this installer consumes them.
const (
	kmsBinary        = "/usr/local/bin/vault-kubernetes-kms"
	kmsService       = "vault-kms"
	kmsEnvFile       = "/etc/kms/kms.env"
	kmsVaultCAFile   = "/etc/kms/vault-ca.crt"
	kmsSocketDir     = "/opt/kms"
	kmsSocket        = "unix:///opt/kms/vaultkms.socket"
	kmsProviderName  = "vault-kubernetes-kms"
	kmsTransitKey    = "etcd"
	kmsApproleRole   = "kms"
	encryptionConfig = "/etc/kubernetes/pki/encryption-config.yaml"  // already mounted into the apiserver pod
	apiServerBackup  = "/etc/kubernetes/kube-apiserver.yaml.pre-kms" // outside the manifests dir on purpose
	encryptionFlag   = "--encryption-provider-config=" + encryptionConfig
)

// EtcdEncryption turns on API server encryption at rest once Vault's KMS is ready.
type EtcdEncryption struct {
	config *config.Config
	exec   executor.ShellExecutor
}

// NewEtcdEncryption builds the installer.
func NewEtcdEncryption(cfg *config.Config, exec executor.ShellExecutor) *EtcdEncryption {
	return &EtcdEncryption{config: cfg, exec: exec}
}

// Install downloads the KMS plugin, runs it as a service, writes the
// EncryptionConfiguration, patches the API server and re-encrypts existing
// Secrets. Every step is idempotent, so a re-run is a no-op.
func (e *EtcdEncryption) Install() error {
	if _, err := e.exec.RunShell("test -f " + kmsEnvFile); err != nil {
		return fmt.Errorf("%s is missing; the Vault installer must run first to configure Transit and the AppRole", kmsEnvFile)
	}
	if err := e.ensureBinary(); err != nil {
		return fmt.Errorf("install KMS plugin binary: %w", err)
	}
	if err := e.ensureService(); err != nil {
		return fmt.Errorf("start KMS plugin service: %w", err)
	}
	if err := e.ensureEncryptionConfig(); err != nil {
		return fmt.Errorf("write EncryptionConfiguration: %w", err)
	}
	patched, err := e.patchAPIServer()
	if err != nil {
		return fmt.Errorf("patch API server: %w", err)
	}
	if !patched {
		fmt.Println("API server already encrypts Secrets; nothing to do")
		return nil
	}
	if err := e.waitForAPIServer(); err != nil {
		return fmt.Errorf("API server did not come back after enabling encryption: %w", err)
	}
	if err := e.reencryptSecrets(); err != nil {
		return fmt.Errorf("re-encrypt existing Secrets: %w", err)
	}
	fmt.Println("etcd encryption at rest is active (Vault KMS v2); existing Secrets re-encrypted")
	return nil
}

// ensureBinary downloads and verifies the arm64/amd64 plugin binary, skipping
// the download when the right version is already installed.
func (e *EtcdEncryption) ensureBinary() error {
	version := e.config.Versions.VaultKMS
	if out, err := e.exec.RunShell(kmsBinary + " --version 2>/dev/null"); err == nil && strings.Contains(out, version) {
		return nil
	}
	arch, err := e.exec.RunShell("dpkg --print-architecture")
	if err != nil {
		return fmt.Errorf("detect architecture: %w", err)
	}
	asset, ok := map[string]string{"arm64": "Linux_arm64", "amd64": "Linux_x86_64"}[strings.TrimSpace(arch)]
	if !ok {
		return fmt.Errorf("unsupported architecture %q for the KMS plugin", strings.TrimSpace(arch))
	}
	base := fmt.Sprintf("https://github.com/FalcoSuessgott/vault-kubernetes-kms/releases/download/v%s", version)
	file := fmt.Sprintf("vault-kubernetes-kms_%s.tar.gz", asset)
	// Download the release and its checksums, verify, then install. The checksum
	// gate matters: this is a third-party binary landing on the control plane.
	// Written for dash (/bin/sh): no pipefail, so the checksum line is matched to
	// a file first — an empty match makes grep fail under `set -e` instead of
	// letting sha256sum pass on no input.
	script := fmt.Sprintf(`set -eu
d=$(mktemp -d); cd "$d"
curl -fsSL -o "%[2]s" "%[1]s/%[2]s"
curl -fsSL -o checksums.txt "%[1]s/checksums.txt"
grep -E "  %[2]s$" checksums.txt > sum.txt
sha256sum -c sum.txt
mkdir x && tar xzf "%[2]s" -C x
install -m0755 "$(find x -type f -name vault-kubernetes-kms | head -1)" %[3]s`,
		base, file, kmsBinary)
	if _, err := e.exec.RunShell(script); err != nil {
		return err
	}
	return nil
}

// ensureService installs and starts the systemd unit that runs the plugin and
// exposes the KMS socket.
func (e *EtcdEncryption) ensureService() error {
	unit := fmt.Sprintf(`[Unit]
Description=Vault KMS plugin (etcd encryption at rest)
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=%s
ExecStartPre=/bin/mkdir -p %s
ExecStart=%s
Restart=on-failure
RestartSec=5
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
`, kmsEnvFile, kmsSocketDir, kmsBinary)
	if err := executor.WriteFile("/etc/systemd/system/"+kmsService+".service", unit); err != nil {
		return err
	}
	if _, err := e.exec.RunShell("systemctl daemon-reload && systemctl enable --now " + kmsService); err != nil {
		return err
	}
	// Wait for the socket and a healthy plugin before the apiserver depends on it.
	deadline := time.Now().Add(shortReadyTimeout)
	for time.Now().Before(deadline) {
		if _, err := e.exec.RunShell("test -S " + kmsSocketDir + "/vaultkms.socket"); err == nil {
			if _, err := e.exec.RunShell("curl -sf http://127.0.0.1:8080/health >/dev/null"); err == nil {
				return nil
			}
		}
		time.Sleep(kmsPollInterval)
	}
	return fmt.Errorf("KMS plugin socket/health not ready within %s", shortReadyTimeout)
}

// ensureEncryptionConfig writes the EncryptionConfiguration into the apiserver's
// already-mounted pki directory.
func (e *EtcdEncryption) ensureEncryptionConfig() error {
	if err := executor.WriteFile(encryptionConfig, encryptionConfigYAML()); err != nil {
		return err
	}
	_, err := e.exec.RunShell("chmod 600 " + encryptionConfig)
	return err
}

// encryptionConfigYAML is the EncryptionConfiguration: KMS v2 (Vault) encrypts
// new writes, and identity stays as a read fallback so any Secret not yet
// migrated is still readable.
func encryptionConfigYAML() string {
	return fmt.Sprintf(`apiVersion: apiserver.config.k8s.io/v1
kind: EncryptionConfiguration
resources:
  - resources:
      - secrets
    providers:
      - kms:
          apiVersion: v2
          name: %s
          endpoint: %s
          timeout: 3s
      - identity: {}
`, kmsProviderName, kmsSocket)
}

// patchAPIServer adds the encryption flag and the KMS socket mount to the static
// pod manifest. It returns false when the manifest is already patched, so no
// restart is triggered on a re-run.
func (e *EtcdEncryption) patchAPIServer() (bool, error) {
	raw, err := os.ReadFile(apiServerManifest)
	if err != nil {
		return false, err
	}
	out, changed, err := patchAPIServerManifest(raw)
	if err != nil || !changed {
		return false, err
	}
	if _, err := e.exec.RunShell(fmt.Sprintf("cp %s %s", apiServerManifest, apiServerBackup)); err != nil {
		return false, fmt.Errorf("back up apiserver manifest: %w", err)
	}
	// The kubelet reloads the static pod when the manifest changes on disk.
	if err := os.WriteFile(apiServerManifest, out, 0o600); err != nil { // #nosec G703 -- constant manifest path
		return false, err
	}
	return true, nil
}

// patchAPIServerManifest adds the encryption flag, the KMS socket volume and its
// mount to a kube-apiserver static pod manifest. It reports changed=false (and
// returns nil output) when the flag is already present, so the caller can skip
// the write and the restart.
//
// It inserts whole lines and never reserializes the YAML. kubeadm writes the
// manifest with its own indentation, and a later installer (keycloak OIDC) edits
// it with sed assuming that exact layout, so round-tripping it through a YAML
// marshaller here would reflow every line and leave that downstream sed inserting
// at the wrong indent — an invalid manifest the kubelet refuses to start. Taking
// the indentation from each anchor line keeps the inserted lines aligned with the
// surrounding kubeadm format and leaves every existing line byte-for-byte intact.
func patchAPIServerManifest(raw []byte) (out []byte, changed bool, err error) {
	if strings.Contains(string(raw), encryptionFlag) {
		return nil, false, nil // already patched
	}
	lines := strings.Split(string(raw), "\n")

	cmdIdx := indexOfTrimmedLine(lines, "- kube-apiserver")
	mountIdx := indexOfTrimmedLine(lines, "volumeMounts:")
	volIdx := indexOfTrimmedLine(lines, "volumes:")
	if cmdIdx < 0 || mountIdx < 0 || volIdx < 0 {
		return nil, false, fmt.Errorf("apiserver manifest is missing the command, volumeMounts or volumes anchor")
	}

	cmdIndent := leadingSpaces(lines[cmdIdx])
	mountIndent := leadingSpaces(lines[mountIdx])
	volIndent := leadingSpaces(lines[volIdx])

	// Insert from the highest anchor down so the earlier indices stay valid.
	lines = insertLinesAfter(lines, volIdx, []string{
		volIndent + "- hostPath:",
		volIndent + "    path: " + kmsSocketDir,
		volIndent + "    type: DirectoryOrCreate",
		volIndent + "  name: kms-socket",
	})
	lines = insertLinesAfter(lines, mountIdx, []string{
		mountIndent + "- mountPath: " + kmsSocketDir,
		mountIndent + "  name: kms-socket",
	})
	lines = insertLinesAfter(lines, cmdIdx, []string{
		cmdIndent + "- " + encryptionFlag,
	})
	return []byte(strings.Join(lines, "\n")), true, nil
}

// waitForAPIServer polls /healthz until the API server is back after the restart.
func (e *EtcdEncryption) waitForAPIServer() error {
	fmt.Println("Waiting for the API server to restart with encryption...")
	time.Sleep(apiServerRestartWait)
	deadline := time.Now().Add(shortReadyTimeout)
	for time.Now().Before(deadline) {
		if _, err := e.exec.RunShell("kubectl get --raw=/healthz 2>/dev/null"); err == nil {
			return nil
		}
		time.Sleep(kmsPollInterval)
	}
	// Roll back so the cluster is not left with an API server that will not start.
	fmt.Println("API server unhealthy; restoring the pre-encryption manifest")
	if _, err := e.exec.RunShell(fmt.Sprintf("cp %s %s", apiServerBackup, apiServerManifest)); err != nil {
		return fmt.Errorf("API server unhealthy and rollback failed: %w", err)
	}
	return fmt.Errorf("API server did not become healthy; rolled back")
}

// reencryptSecrets rewrites every Secret so the ones created before encryption
// was on are stored through the KMS provider too.
func (e *EtcdEncryption) reencryptSecrets() error {
	fmt.Println("Re-encrypting existing Secrets...")
	_, err := e.exec.RunShell("kubectl get secrets --all-namespaces -o json | kubectl replace -f -")
	return err
}

// indexOfTrimmedLine returns the first line whose whitespace-trimmed content
// equals want, or -1. Each anchor (the command entry, the volumeMounts and the
// volumes keys) appears once in a kube-apiserver manifest.
func indexOfTrimmedLine(lines []string, want string) int {
	for i, l := range lines {
		if strings.TrimSpace(l) == want {
			return i
		}
	}
	return -1
}

// leadingSpaces returns the run of spaces a line begins with, so inserted lines
// can match the indentation of the anchor they follow.
func leadingSpaces(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " "))]
}

// insertLinesAfter returns lines with add spliced in right after index idx.
func insertLinesAfter(lines []string, idx int, add []string) []string {
	out := make([]string, 0, len(lines)+len(add))
	out = append(out, lines[:idx+1]...)
	out = append(out, add...)
	out = append(out, lines[idx+1:]...)
	return out
}
