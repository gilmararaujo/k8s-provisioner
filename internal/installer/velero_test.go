package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func veleroConfig() *config.Config {
	c := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
	c.Backup.Velero.Enabled = true
	c.Vault.Enabled = true
	c.Versions.Velero = "12.2.5"
	c.Versions.VeleroPluginAWS = "v1.14.1"
	return c
}

func TestVeleroValues_TargetsSeaweedFS(t *testing.T) {
	v := NewVelero(veleroConfig(), &fakeShell{})
	out := v.values()

	assert.Contains(t, out, "velero/velero-plugin-for-aws:v1.14.1", "pins the AWS plugin image")
	assert.Contains(t, out, "existingSecret: "+veleroCredSecret, "reads creds from the secret we create")
	assert.Contains(t, out, "bucket: velero", "backs up to the velero bucket")
	assert.Contains(t, out, "s3Url: http://192.168.56.20:8333", "points at the SeaweedFS endpoint")
	assert.Contains(t, out, `s3ForcePathStyle: "true"`, "path-style addressing for a non-AWS S3")
	assert.Contains(t, out, "defaultVolumesToFsBackup: true", "PV data via kopia (no CSI snapshot on NFS)")
	assert.Contains(t, out, "deployNodeAgent: true", "node-agent runs the filesystem backups")
	assert.Contains(t, out, "snapshotsEnabled: false", "no volume snapshot location")
	assert.Contains(t, out, `schedule: "0 3 * * *"`, "default daily schedule")
	assert.Contains(t, out, "ttl: 168h0m0s", "default retention")
}

func TestVeleroCredentialsSecret_IsAwsIni(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewVelero(veleroConfig(), sh).createCredentialsSecret("AK", "SK"))

	require.Len(t, sh.stdins, 1)
	m := sh.stdins[0]
	assert.Contains(t, m, "kind: Secret")
	assert.Contains(t, m, "name: "+veleroCredSecret)
	assert.Contains(t, m, "namespace: "+veleroNamespace)
	assert.Contains(t, m, "[default]", "AWS shared-credentials INI format")
	assert.Contains(t, m, "aws_access_key_id=AK")
	assert.Contains(t, m, "aws_secret_access_key=SK")
}

func TestVeleroEnabled(t *testing.T) {
	assert.True(t, veleroConfig().VeleroEnabled())

	noVault := veleroConfig()
	noVault.Vault.Enabled = false
	assert.False(t, noVault.VeleroEnabled(), "needs Vault for the S3 credentials")

	noStorage := veleroConfig()
	noStorage.Nodes = nil
	assert.False(t, noStorage.VeleroEnabled(), "needs the storage node that runs SeaweedFS")

	off := veleroConfig()
	off.Backup.Velero.Enabled = false
	assert.False(t, off.VeleroEnabled())
}

func TestVeleroDefaults(t *testing.T) {
	c := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
	assert.Equal(t, "0 3 * * *", c.BackupVeleroSchedule())
	assert.Equal(t, "168h0m0s", c.BackupVeleroTTL())
}
