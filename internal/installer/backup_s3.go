package installer

import (
	"fmt"
	"strings"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

const (
	seaweedAdminEnvRemote = "/etc/seaweedfs/s3-admin.env" // written by vagrant/scripts/storage-seaweedfs.sh
	backupS3VaultPath     = "backup-s3"                   // Vault KV secret/data/backup-s3, shared by etcd-backup and Velero
)

// resolveBackupS3Creds returns the SeaweedFS S3 access/secret key for the backup
// store. It prefers Vault (where the first consumer mirrors them) and falls back
// to reading them off the storage node over SSH, mirroring them into Vault so the
// next consumer finds them there. Both EtcdBackup and Velero use this.
func resolveBackupS3Creds(cfg *config.Config, exec executor.ShellExecutor) (accessKey, secretKey string, err error) {
	if ak, sk := readBackupS3FromVault(cfg); ak != "" && sk != "" {
		return ak, sk, nil
	}
	ak, sk, err := fetchSeaweedCredsFromStorage(cfg, exec)
	if err != nil {
		return "", "", err
	}
	if werr := storeBackupS3InVault(cfg, ak, sk); werr != nil {
		fmt.Printf("Warning: could not mirror S3 creds into Vault (%v)\n", werr)
	}
	return ak, sk, nil
}

// readBackupS3FromVault returns the stored creds, or empty strings if Vault is
// unreachable or the secret is absent.
func readBackupS3FromVault(cfg *config.Config) (accessKey, secretKey string) {
	token := ResolveVaultToken(cfg.Vault.Token)
	if token == "" {
		return "", ""
	}
	m, err := NewVaultClient(cfg.VaultAddress(), token, cfg.VaultCACert()).ReadSecret(backupS3VaultPath)
	if err != nil {
		return "", ""
	}
	return m["access_key"], m["secret_key"]
}

// fetchSeaweedCredsFromStorage reads the SeaweedFS admin credentials off the
// storage node over SSH (the same transport Vault init data uses).
func fetchSeaweedCredsFromStorage(cfg *config.Config, exec executor.ShellExecutor) (accessKey, secretKey string, err error) {
	env, opts, user, usesPassword := sshConnFor(cfg)
	if usesPassword {
		if _, e := exec.RunShell("apt-get install -y sshpass 2>/dev/null || true"); e != nil {
			fmt.Printf("Warning: could not install sshpass: %v\n", e)
		}
	}
	cmd := fmt.Sprintf("%sssh %s %s@%s 'sudo cat %s'", env, opts, user, cfg.StorageIP(), seaweedAdminEnvRemote)
	out, err := exec.RunShell(cmd)
	if err != nil {
		return "", "", err
	}
	accessKey, secretKey = parseEnvValue(out, "S3_ACCESS_KEY"), parseEnvValue(out, "S3_SECRET_KEY")
	if accessKey == "" || secretKey == "" {
		return "", "", fmt.Errorf("S3_ACCESS_KEY/S3_SECRET_KEY not found in %s", seaweedAdminEnvRemote)
	}
	return accessKey, secretKey, nil
}

func storeBackupS3InVault(cfg *config.Config, accessKey, secretKey string) error {
	token := ResolveVaultToken(cfg.Vault.Token)
	if token == "" {
		return fmt.Errorf("no Vault token available")
	}
	return NewVaultClient(cfg.VaultAddress(), token, cfg.VaultCACert()).
		WriteSecret(backupS3VaultPath, map[string]string{
			"access_key":    accessKey,
			"secret_key":    secretKey,
			"endpoint":      cfg.BackupS3Endpoint(),
			"bucket_etcd":   cfg.BackupEtcdBucket(),
			"bucket_velero": cfg.BackupVeleroBucket(),
		})
}

// parseEnvValue returns the value of KEY=VALUE from an env-file body, trimming
// surrounding quotes. Returns "" when the key is absent.
func parseEnvValue(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+"="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
