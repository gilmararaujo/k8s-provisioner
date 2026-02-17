package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type NFSProvisioner struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewNFSProvisioner(cfg *config.Config, exec executor.CommandExecutor) *NFSProvisioner {
	return &NFSProvisioner{config: cfg, exec: exec}
}

func (n *NFSProvisioner) Install() error {
	fmt.Println("Installing NFS Storage Provisioner...")

	// Install Helm if not present
	if err := n.installHelm(); err != nil {
		return err
	}

	// Create static StorageClass (for manual PV/PVC)
	fmt.Println("Creating nfs-static StorageClass...")
	if err := n.createStaticStorageClass(); err != nil {
		return err
	}

	// Install dynamic provisioner
	fmt.Println("Installing NFS dynamic provisioner...")
	if err := n.installDynamicProvisioner(); err != nil {
		return err
	}

	// Wait for provisioner to be ready
	fmt.Println("Waiting for NFS provisioner to be ready...")
	if err := n.waitForReady(DefaultReadyTimeout); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	fmt.Println("NFS Storage Provisioner installed successfully!")
	n.printStorageInfo()
	return nil
}

func (n *NFSProvisioner) createStaticStorageClass() error {
	staticSC := `apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: nfs-static
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: WaitForFirstConsumer
reclaimPolicy: Retain`

	if err := executor.WriteFile("/tmp/nfs-static-sc.yaml", staticSC); err != nil {
		return err
	}

	// Delete existing nfs-storage if exists (we're replacing it)
	_, _ = n.exec.RunShell("kubectl delete storageclass nfs-storage 2>/dev/null || true")

	_, err := n.exec.RunShell("kubectl apply -f /tmp/nfs-static-sc.yaml")
	return err
}

func (n *NFSProvisioner) installDynamicProvisioner() error {
	nfsServer := n.config.Storage.NFSServer
	if nfsServer == "" {
		nfsServer = "storage"
	}
	nfsPath := n.config.Storage.NFSPath
	if nfsPath == "" {
		nfsPath = "/exports/k8s-volumes"
	}

	// Resolve hostname to IP if needed
	nfsIP, err := n.resolveNFSServer(nfsServer)
	if err != nil {
		return fmt.Errorf("failed to resolve NFS server: %w", err)
	}

	// Add Helm repo
	if _, err := n.exec.RunShell("helm repo add nfs-subdir-external-provisioner https://kubernetes-sigs.github.io/nfs-subdir-external-provisioner"); err != nil {
		return err
	}
	if _, err := n.exec.RunShell("helm repo update"); err != nil {
		return err
	}

	// Create namespace
	_, _ = n.exec.RunShell("kubectl create namespace nfs-provisioner 2>/dev/null || true")

	// Install the provisioner (single line to avoid shell interpretation issues)
	helmCmd := fmt.Sprintf("helm upgrade --install nfs-provisioner nfs-subdir-external-provisioner/nfs-subdir-external-provisioner --namespace nfs-provisioner --set nfs.server=%s --set nfs.path=%s --set storageClass.name=nfs-dynamic --set storageClass.defaultClass=%t --set storageClass.reclaimPolicy=Delete --set storageClass.archiveOnDelete=true",
		nfsIP, nfsPath, n.config.Storage.DefaultDynamic)

	return n.exec.RunShellWithOutput(helmCmd)
}

func (n *NFSProvisioner) resolveNFSServer(server string) (string, error) {
	// Check if it's already an IP
	out, err := n.exec.RunShell(fmt.Sprintf("getent hosts %s | awk '{print $1}'", server))
	if err != nil || strings.TrimSpace(out) == "" {
		// Try to get from /etc/hosts
		out, err = n.exec.RunShell(fmt.Sprintf("grep -w %s /etc/hosts | awk '{print $1}' | head -1", server))
		if err != nil || strings.TrimSpace(out) == "" {
			return server, nil // Return as-is, might be an IP already
		}
	}
	return strings.TrimSpace(out), nil
}

func (n *NFSProvisioner) installHelm() error {
	// Check if helm is already installed
	if _, err := n.exec.RunShell("which helm"); err == nil {
		return nil
	}

	fmt.Println("Installing Helm...")
	installCmd := "curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash"
	if err := n.exec.RunShellWithOutput(installCmd); err != nil {
		return fmt.Errorf("failed to install Helm: %w", err)
	}

	return nil
}

func (n *NFSProvisioner) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := n.exec.RunShell("kubectl get pods -n nfs-provisioner -l app=nfs-subdir-external-provisioner -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if err == nil && out == "Running" {
			return nil
		}
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for NFS provisioner")
}

func (n *NFSProvisioner) printStorageInfo() {
	fmt.Println("\n========================================")
	fmt.Println("NFS Storage Configuration")
	fmt.Println("========================================")
	fmt.Println("\nStorageClasses available:")
	fmt.Println("  - nfs-dynamic: Automatic PV provisioning")
	fmt.Println("  - nfs-static:  Manual PV/PVC creation")
	fmt.Println("\nUsage examples:")
	fmt.Println("\n  Dynamic (automatic):")
	fmt.Println("    spec:")
	fmt.Println("      storageClassName: nfs-dynamic")
	fmt.Println("\n  Static (manual PV required):")
	fmt.Println("    spec:")
	fmt.Println("      storageClassName: nfs-static")
	fmt.Println("========================================")
}
