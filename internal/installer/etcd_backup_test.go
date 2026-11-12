package installer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestParseEnvValue(t *testing.T) {
	body := "S3_ENDPOINT=http://192.168.56.20:8333\nS3_ACCESS_KEY=seaweed-admin\nS3_SECRET_KEY=\"abc123\"\n"
	assert.Equal(t, "http://192.168.56.20:8333", parseEnvValue(body, "S3_ENDPOINT"))
	assert.Equal(t, "seaweed-admin", parseEnvValue(body, "S3_ACCESS_KEY"))
	assert.Equal(t, "abc123", parseEnvValue(body, "S3_SECRET_KEY"), "surrounding quotes are trimmed")
	assert.Equal(t, "", parseEnvValue(body, "MISSING"))
	assert.Equal(t, "", parseEnvValue(body, "S3_ACCESS"), "must match the whole key, not a prefix")
}

func TestS3EnvFileContent(t *testing.T) {
	out := s3EnvFileContent("http://s:8333", "ak", "sk", "etcd-backups", 7)
	assert.Contains(t, out, "S3_ENDPOINT=http://s:8333\n")
	assert.Contains(t, out, "S3_ACCESS_KEY=ak\n")
	assert.Contains(t, out, "S3_SECRET_KEY=sk\n")
	assert.Contains(t, out, "S3_BUCKET=etcd-backups\n")
	assert.Contains(t, out, "RETENTION_DAYS=7\n")
}

func TestEtcdBackupScript_TakesSnapshotAndUploads(t *testing.T) {
	s := etcdBackupScriptBody
	assert.Contains(t, s, ". /etc/etcd-backup/s3.env", "reads credentials/settings from the env file")
	assert.Contains(t, s, "snapshot save", "takes an etcd snapshot")
	assert.Contains(t, s, "/etc/kubernetes/pki/etcd/server.crt", "authenticates to etcd with its server cert")
	assert.Contains(t, s, "-l component=etcd", "selects the etcd static pod")
	assert.Contains(t, s, `aws --endpoint-url "$S3_ENDPOINT" s3 cp`, "uploads to the S3 endpoint")
	assert.Contains(t, s, "RETENTION_DAYS", "prunes old snapshots")
}

func TestFetchSeaweedCreds_ReadsFromStorageOverSSH(t *testing.T) {
	cfg := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
	sh := &fakeShell{outputs: map[string]string{
		seaweedAdminEnvRemote: "S3_ENDPOINT=http://192.168.56.20:8333\nS3_ACCESS_KEY=seaweed-admin\nS3_SECRET_KEY=sekret\n",
	}}
	ak, sk, err := fetchSeaweedCredsFromStorage(cfg, sh)
	require.NoError(t, err)
	assert.Equal(t, "seaweed-admin", ak)
	assert.Equal(t, "sekret", sk)

	joined := strings.Join(sh.calls, "\n")
	assert.Contains(t, joined, "ssh", "reaches the storage node over SSH")
	assert.Contains(t, joined, "192.168.56.20", "targets the storage node IP")
	assert.Contains(t, joined, "sudo cat "+seaweedAdminEnvRemote)
}

func TestFetchSeaweedCreds_ErrorsWhenMissing(t *testing.T) {
	cfg := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
	sh := &fakeShell{outputs: map[string]string{seaweedAdminEnvRemote: "S3_ENDPOINT=http://x\n"}}
	_, _, err := fetchSeaweedCredsFromStorage(cfg, sh)
	assert.ErrorContains(t, err, "not found")
}

func TestEtcdBackupEnabled(t *testing.T) {
	base := func() *config.Config {
		c := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
		c.Backup.Etcd.Enabled = true
		c.Vault.Enabled = true
		return c
	}
	assert.True(t, base().EtcdBackupEnabled())

	noVault := base()
	noVault.Vault.Enabled = false
	assert.False(t, noVault.EtcdBackupEnabled(), "needs Vault for the S3 credentials")

	noStorage := base()
	noStorage.Nodes = nil
	assert.False(t, noStorage.EtcdBackupEnabled(), "needs the storage node that runs SeaweedFS")

	off := base()
	off.Backup.Etcd.Enabled = false
	assert.False(t, off.EtcdBackupEnabled())
}

func TestBackupDefaults(t *testing.T) {
	c := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
	assert.Equal(t, 8333, c.BackupS3Port())
	assert.Equal(t, "http://192.168.56.20:8333", c.BackupS3Endpoint())
	assert.Equal(t, "etcd-backups", c.BackupEtcdBucket())
	assert.Equal(t, "velero", c.BackupVeleroBucket())
	assert.Equal(t, "*-*-* 02:00:00", c.EtcdBackupSchedule())
	assert.Equal(t, 7, c.EtcdBackupRetentionDays())
}
