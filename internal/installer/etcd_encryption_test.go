package installer

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// a minimal kube-apiserver static pod manifest, as kubeadm writes it.
const sampleAPIServerManifest = `apiVersion: v1
kind: Pod
metadata:
  name: kube-apiserver
  namespace: kube-system
spec:
  containers:
  - name: kube-apiserver
    image: registry.k8s.io/kube-apiserver:v1.37.2
    command:
    - kube-apiserver
    - --advertise-address=192.168.56.10
    - --allow-privileged=true
    volumeMounts:
    - mountPath: /etc/kubernetes/pki
      name: k8s-certs
      readOnly: true
  hostNetwork: true
  volumes:
  - hostPath:
      path: /etc/kubernetes/pki
      type: DirectoryOrCreate
    name: k8s-certs
`

func TestPatchAPIServerManifest_AddsFlagVolumeAndMount(t *testing.T) {
	out, changed, err := patchAPIServerManifest([]byte(sampleAPIServerManifest))
	require.NoError(t, err)
	require.True(t, changed)

	var doc struct {
		Spec struct {
			Containers []struct {
				Command      []string `yaml:"command"`
				VolumeMounts []struct {
					Name      string `yaml:"name"`
					MountPath string `yaml:"mountPath"`
				} `yaml:"volumeMounts"`
			} `yaml:"containers"`
			Volumes []struct {
				Name     string `yaml:"name"`
				HostPath struct {
					Path string `yaml:"path"`
					Type string `yaml:"type"`
				} `yaml:"hostPath"`
			} `yaml:"volumes"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal(out, &doc))
	c := doc.Spec.Containers[0]

	assert.Contains(t, c.Command, encryptionFlag, "the encryption-provider-config flag is added")
	assert.Contains(t, c.Command, "--advertise-address=192.168.56.10", "existing flags are preserved")

	var mount, volume bool
	for _, m := range c.VolumeMounts {
		if m.Name == "kms-socket" && m.MountPath == kmsSocketDir {
			mount = true
		}
	}
	for _, v := range doc.Spec.Volumes {
		if v.Name == "kms-socket" && v.HostPath.Path == kmsSocketDir && v.HostPath.Type == "DirectoryOrCreate" {
			volume = true
		}
	}
	assert.True(t, mount, "the KMS socket is mounted into the apiserver")
	assert.True(t, volume, "the KMS socket dir is a hostPath volume")

	// The patch must preserve kubeadm's layout line-for-line: a later installer
	// (keycloak OIDC) edits this manifest with sed assuming that exact
	// indentation, so reflowing it here would break that downstream patch.
	s := string(out)
	assert.Contains(t, s, "\n    - --advertise-address=192.168.56.10\n", "existing lines keep their original indentation")
	assert.Contains(t, s, "\n  containers:\n", "the 2-space kubeadm layout is untouched")
	assert.Contains(t, s, "\n    - "+encryptionFlag+"\n", "the flag is inserted at the command's indent")
	assert.Contains(t, s, "\n    - mountPath: "+kmsSocketDir+"\n      name: kms-socket\n", "the mount matches the volumeMounts indent")
	assert.Contains(t, s, "\n  - hostPath:\n      path: "+kmsSocketDir+"\n      type: DirectoryOrCreate\n    name: kms-socket\n", "the volume matches the volumes indent")
}

func TestPatchAPIServerManifest_Idempotent(t *testing.T) {
	once, changed, err := patchAPIServerManifest([]byte(sampleAPIServerManifest))
	require.NoError(t, err)
	require.True(t, changed)

	out, changed, err := patchAPIServerManifest(once)
	require.NoError(t, err)
	assert.False(t, changed, "a second patch is a no-op")
	assert.Nil(t, out, "no rewrite when already patched")
}

func TestEncryptionConfigYAML_KMSFirstIdentityFallback(t *testing.T) {
	var cfg struct {
		Resources []struct {
			Resources []string         `yaml:"resources"`
			Providers []map[string]any `yaml:"providers"`
		} `yaml:"resources"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(encryptionConfigYAML()), &cfg))
	require.Len(t, cfg.Resources, 1)
	r := cfg.Resources[0]
	assert.Equal(t, []string{"secrets"}, r.Resources)
	require.Len(t, r.Providers, 2)
	kms, _ := r.Providers[0]["kms"].(map[string]any)
	require.NotNil(t, kms, "KMS must be the first provider so new writes are encrypted")
	assert.Equal(t, "v2", kms["apiVersion"])
	assert.Equal(t, kmsProviderName, kms["name"])
	assert.Equal(t, kmsSocket, kms["endpoint"])
	_, hasIdentity := r.Providers[1]["identity"]
	assert.True(t, hasIdentity, "identity is the read fallback for not-yet-migrated Secrets")
}

func TestEtcdEncryption_RequiresKMSEnv(t *testing.T) {
	// With `test -f <kms.env>` failing, Install stops before touching anything.
	sh := &fakeShell{
		outputs: map[string]string{"test -f": ""},
		errs:    map[string]error{"test -f": errors.New("missing")},
	}
	err := NewEtcdEncryption(auditTestConfig(), sh).Install()
	require.Error(t, err)
	assert.Contains(t, err.Error(), kmsEnvFile)
}

func TestEnsureBinary_VerifiesChecksumAndPicksArch(t *testing.T) {
	// The download script must verify the checksum and be dash-safe (no pipefail).
	// The version check returns nothing, so ensureBinary takes the download path.
	sh := &fakeShell{outputs: map[string]string{"dpkg --print-architecture": "arm64"}}
	cfg := auditTestConfig()
	cfg.Versions.VaultKMS = "1.4.0"
	_ = NewEtcdEncryption(cfg, sh).ensureBinary()

	var script string
	for _, c := range sh.calls {
		if strings.Contains(c, "sha256sum") {
			script = c
		}
	}
	require.NotEmpty(t, script, "the download script ran")
	assert.Contains(t, script, "sha256sum -c sum.txt", "the checksum is verified before install")
	assert.NotContains(t, script, "pipefail", "dash (/bin/sh) has no pipefail")
	assert.Contains(t, script, "vault-kubernetes-kms_Linux_arm64.tar.gz", "arch maps arm64 to the right asset")
	assert.Contains(t, script, "v1.4.0", "the pinned version is used")
}
