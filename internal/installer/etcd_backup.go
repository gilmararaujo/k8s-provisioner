package installer

import (
	_ "embed"
	"fmt"
	"os"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

// etcd-backup.sh is embedded (not generated) so it can be edited and shell-checked
// as a real script; it reads its settings at runtime from the env file below.
//
//go:embed scripts/etcd-backup.sh
var etcdBackupScriptBody string

const (
	etcdBackupScriptPath = "/usr/local/bin/etcd-backup.sh"
	etcdBackupEnvDir     = "/etc/etcd-backup"
	etcdBackupEnvFile    = "/etc/etcd-backup/s3.env"
	etcdBackupUnit       = "etcd-backup"
)

// EtcdBackup installs a control-plane systemd timer that snapshots etcd and ships
// the snapshot to the SeaweedFS S3 bucket on the storage node. The S3 credentials
// are read from the storage node (where storage-seaweedfs.sh generated them),
// mirrored into Vault for Velero to share, and written to a root-only env file the
// backup script sources.
type EtcdBackup struct {
	config *config.Config
	exec   executor.ShellExecutor
}

func NewEtcdBackup(cfg *config.Config, exec executor.ShellExecutor) *EtcdBackup {
	return &EtcdBackup{config: cfg, exec: exec}
}

func (b *EtcdBackup) Install() error {
	if !b.config.EtcdBackupEnabled() {
		fmt.Println("etcd backup disabled (backup.etcd.enabled=false, or no storage node / Vault); skipping")
		return nil
	}
	fmt.Println("Installing etcd snapshot backup (control-plane timer -> SeaweedFS S3)...")

	// resolveBackupS3Creds (shared with Velero) reads the SeaweedFS creds, preferring
	// Vault and falling back to the storage node, mirroring them into Vault either way.
	accessKey, secretKey, err := resolveBackupS3Creds(b.config, b.exec)
	if err != nil {
		return fmt.Errorf("resolve SeaweedFS S3 credentials: %w", err)
	}
	endpoint, bucket := b.config.BackupS3Endpoint(), b.config.BackupEtcdBucket()

	if err := b.writeEnvFile(endpoint, accessKey, secretKey, bucket); err != nil {
		return fmt.Errorf("write %s: %w", etcdBackupEnvFile, err)
	}
	if err := b.ensureAwsCLI(); err != nil {
		return fmt.Errorf("install aws CLI: %w", err)
	}
	if err := b.installUnits(); err != nil {
		return err
	}

	// Take one snapshot now to validate the whole path end-to-end. Non-fatal: the
	// timer will retry on schedule even if this first run fails.
	if _, err := b.exec.RunShell("systemctl start " + etcdBackupUnit + ".service"); err != nil {
		fmt.Printf("Warning: initial etcd backup run failed (%v) - the timer will retry\n", err)
	} else {
		fmt.Println("Initial etcd snapshot taken and uploaded.")
	}
	fmt.Printf("etcd backup installed: %s -> s3://%s (schedule %q, retention %dd)\n",
		etcdBackupScriptPath, bucket, b.config.EtcdBackupSchedule(), b.config.EtcdBackupRetentionDays())
	return nil
}

// fetchS3Creds reads the SeaweedFS admin credentials from the storage node over
// writeEnvFile writes the root-only env file the backup script sources.
func (b *EtcdBackup) writeEnvFile(endpoint, accessKey, secretKey, bucket string) error {
	if err := os.MkdirAll(etcdBackupEnvDir, 0o700); err != nil {
		return err
	}
	content := s3EnvFileContent(endpoint, accessKey, secretKey, bucket, b.config.EtcdBackupRetentionDays())
	return os.WriteFile(etcdBackupEnvFile, []byte(content), 0o600)
}

func (b *EtcdBackup) ensureAwsCLI() error {
	if _, err := b.exec.RunShell("command -v aws >/dev/null 2>&1"); err == nil {
		return nil
	}
	return b.exec.RunShellWithOutput("apt-get install -y awscli")
}

func (b *EtcdBackup) installUnits() error {
	// #nosec G306 -- a root-run systemd script; must be executable.
	if err := os.WriteFile(etcdBackupScriptPath, []byte(etcdBackupScriptBody), 0o700); err != nil {
		return err
	}
	svc := "/etc/systemd/system/" + etcdBackupUnit + ".service"
	if err := os.WriteFile(svc, []byte(etcdBackupServiceUnit), 0o600); err != nil {
		return err
	}
	timer := "/etc/systemd/system/" + etcdBackupUnit + ".timer"
	if err := os.WriteFile(timer, []byte(fmt.Sprintf(etcdBackupTimerUnit, b.config.EtcdBackupSchedule())), 0o600); err != nil {
		return err
	}
	_, err := b.exec.RunShell("systemctl daemon-reload && systemctl enable --now " + etcdBackupUnit + ".timer")
	return err
}

// s3EnvFileContent builds the root-only env file the backup script sources.
func s3EnvFileContent(endpoint, accessKey, secretKey, bucket string, retentionDays int) string {
	return fmt.Sprintf("S3_ENDPOINT=%s\nS3_ACCESS_KEY=%s\nS3_SECRET_KEY=%s\nS3_BUCKET=%s\nRETENTION_DAYS=%d\n",
		endpoint, accessKey, secretKey, bucket, retentionDays)
}

const etcdBackupServiceUnit = `[Unit]
Description=etcd snapshot backup to SeaweedFS S3
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=` + etcdBackupScriptPath + `
`

const etcdBackupTimerUnit = `[Unit]
Description=Scheduled etcd snapshot backup

[Timer]
OnCalendar=%s
Persistent=true
AccuracySec=1min

[Install]
WantedBy=timers.target
`
