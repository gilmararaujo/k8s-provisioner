package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Calico struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewCalico(cfg *config.Config, exec executor.CommandExecutor) *Calico {
	return &Calico{config: cfg, exec: exec}
}

func (c *Calico) Install() error {
	version := c.config.Versions.Calico

	// Install Tigera operator
	fmt.Printf("Installing Tigera operator (Calico %s)...\n", version)
	operatorURL := fmt.Sprintf("https://raw.githubusercontent.com/projectcalico/calico/v%s/manifests/tigera-operator.yaml", version)
	if _, err := c.exec.RunShell(fmt.Sprintf("kubectl create -f %s", operatorURL)); err != nil {
		return err
	}

	// Wait for CRDs
	fmt.Println("Waiting for Tigera CRDs...")
	time.Sleep(CRDInitialDelay)

	// Create Calico installation
	installation := fmt.Sprintf(`apiVersion: operator.tigera.io/v1
kind: Installation
metadata:
  name: default
spec:
  calicoNetwork:
    ipPools:
    - blockSize: 26
      cidr: %s
      encapsulation: VXLANCrossSubnet
      natOutgoing: Enabled
      nodeSelector: all()
---
apiVersion: operator.tigera.io/v1
kind: APIServer
metadata:
  name: default
spec: {}`, c.config.Cluster.PodCIDR)

	if err := executor.WriteFile("/tmp/calico-installation.yaml", installation); err != nil {
		return err
	}

	if _, err := c.exec.RunShell("kubectl apply -f /tmp/calico-installation.yaml"); err != nil {
		return err
	}

	// Wait for Calico to be ready
	fmt.Println("Waiting for Calico to be ready...")
	return c.waitForReady(DefaultReadyTimeout)
}

func (c *Calico) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := c.exec.RunShell("kubectl get pods -n calico-system -l k8s-app=calico-node -o jsonpath='{.items[*].status.phase}'")
		if err == nil && out == "Running" {
			fmt.Println("Calico is ready!")
			return nil
		}
		fmt.Println("Waiting for Calico pods...")
		time.Sleep(LongPollInterval)
	}
	// Don't fail, just warn
	fmt.Println("Warning: Calico pods may still be starting")
	return nil
}