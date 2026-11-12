package installer

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

const (
	veleroNamespace  = "velero"
	veleroCredSecret = "cloud-credentials"
	veleroS3Region   = "us-east-1" // SeaweedFS ignores the region, but the AWS plugin requires one
	veleroValuesPath = "/tmp/velero-values.yaml"
)

// Velero installs Velero via its official Helm chart, backing up to the SeaweedFS
// S3 store on the storage node. PV data is captured with the node-agent (kopia
// File System Backup) because the NFS StorageClasses have no CSI snapshot support.
// S3 credentials come from Vault (shared with the etcd backup via secret/data/backup-s3).
type Velero struct {
	config *config.Config
	exec   executor.ShellExecutor
}

func NewVelero(cfg *config.Config, exec executor.ShellExecutor) *Velero {
	return &Velero{config: cfg, exec: exec}
}

func (v *Velero) Install() error {
	if !v.config.VeleroEnabled() {
		fmt.Println("Velero disabled (backup.velero.enabled=false, or no storage node / Vault); skipping")
		return nil
	}
	fmt.Println("Installing Velero (backups -> SeaweedFS S3, PV data via node-agent/kopia)...")

	accessKey, secretKey, err := resolveBackupS3Creds(v.config, v.exec)
	if err != nil {
		return fmt.Errorf("resolve SeaweedFS S3 credentials: %w", err)
	}
	if err := v.ensureHelm(); err != nil {
		return fmt.Errorf("install Helm: %w", err)
	}
	if err := v.createCredentialsSecret(accessKey, secretKey); err != nil {
		return fmt.Errorf("create %s secret: %w", veleroCredSecret, err)
	}
	if err := v.helmInstall(); err != nil {
		return fmt.Errorf("helm install velero: %w", err)
	}
	fmt.Printf("Velero installed: s3://%s @ %s (daily %q, ttl %s)\n",
		v.config.BackupVeleroBucket(), v.config.BackupS3Endpoint(),
		v.config.BackupVeleroSchedule(), v.config.BackupVeleroTTL())
	return nil
}

func (v *Velero) ensureHelm() error {
	if _, err := v.exec.RunShell("helm version 2>/dev/null"); err == nil {
		return nil
	}
	fmt.Println("Installing Helm...")
	_, err := v.exec.RunShell("curl -fsSL --connect-timeout 10 --max-time 300 https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash")
	return err
}

// createCredentialsSecret writes the Secret the AWS plugin reads: an AWS
// shared-credentials INI under key "cloud", in the velero namespace.
func (v *Velero) createCredentialsSecret(accessKey, secretKey string) error {
	ini := fmt.Sprintf("[default]\naws_access_key_id=%s\naws_secret_access_key=%s\n", accessKey, secretKey)
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: v1
kind: Secret
metadata:
  name: %[2]s
  namespace: %[1]s
type: Opaque
stringData:
  cloud: %[3]q`, veleroNamespace, veleroCredSecret, ini)
	_, err := v.exec.RunShellWithStdin("kubectl apply -f -", manifest)
	return err
}

func (v *Velero) helmInstall() error {
	if _, err := v.exec.RunShell("helm repo add vmware-tanzu https://vmware-tanzu.github.io/helm-charts 2>/dev/null || true"); err != nil {
		fmt.Printf("Warning: could not add velero Helm repo: %v\n", err)
	}
	if _, err := v.exec.RunShell("helm repo update vmware-tanzu"); err != nil {
		fmt.Printf("Warning: helm repo update failed: %v\n", err)
	}
	if err := executor.WriteFile(veleroValuesPath, v.values()); err != nil {
		return err
	}
	cmd := fmt.Sprintf(
		"helm upgrade --install velero vmware-tanzu/velero -n %s --create-namespace"+
			" --version %s -f %s --wait --timeout=5m",
		veleroNamespace, v.config.Versions.Velero, veleroValuesPath,
	)
	_, err := v.exec.RunShell(cmd)
	return err
}

// values renders the Helm values: the AWS plugin init container, the SeaweedFS
// BackupStorageLocation (path-style S3 at the storage node), the node-agent for
// filesystem backups, and a default daily schedule covering every namespace.
func (v *Velero) values() string {
	return fmt.Sprintf(`initContainers:
  - name: velero-plugin-for-aws
    image: velero/velero-plugin-for-aws:%[1]s
    imagePullPolicy: IfNotPresent
    volumeMounts:
      - mountPath: /target
        name: plugins
credentials:
  useSecret: true
  existingSecret: %[2]s
configuration:
  backupStorageLocation:
    - name: default
      provider: aws
      bucket: %[3]s
      default: true
      config:
        region: %[4]s
        s3ForcePathStyle: "true"
        s3Url: %[5]s
  volumeSnapshotLocation: []
  defaultVolumesToFsBackup: true
snapshotsEnabled: false
deployNodeAgent: true
schedules:
  daily:
    disabled: false
    schedule: %[6]q
    template:
      ttl: %[7]s
      storageLocation: default
      includedNamespaces:
        - '*'
`, v.config.Versions.VeleroPluginAWS, veleroCredSecret, v.config.BackupVeleroBucket(),
		veleroS3Region, v.config.BackupS3Endpoint(), v.config.BackupVeleroSchedule(), v.config.BackupVeleroTTL())
}
