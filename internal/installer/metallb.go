package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type MetalLB struct {
	config *config.Config
	exec   *executor.Executor
}

func NewMetalLB(cfg *config.Config, exec *executor.Executor) *MetalLB {
	return &MetalLB{config: cfg, exec: exec}
}

func (m *MetalLB) Install() error {
	version := m.config.Versions.MetalLB

	// Install MetalLB
	fmt.Printf("Installing MetalLB %s...\n", version)
	manifestURL := fmt.Sprintf("https://raw.githubusercontent.com/metallb/metallb/v%s/config/manifests/metallb-native.yaml", version)
	if _, err := m.exec.RunShell(fmt.Sprintf("kubectl apply -f %s", manifestURL)); err != nil {
		return err
	}

	// Wait for MetalLB controller to be ready
	fmt.Println("Waiting for MetalLB controller...")
	if err := m.waitForReady(5 * time.Minute); err != nil {
		return err
	}

	// Wait for webhook to stabilize
	fmt.Println("Waiting for MetalLB webhook to stabilize...")
	time.Sleep(30 * time.Second)

	// Configure IPAddressPool and L2Advertisement
	return m.configure()
}

func (m *MetalLB) configure() error {
	fmt.Println("Configuring MetalLB IP pool...")

	config := fmt.Sprintf(`apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: default-pool
  namespace: metallb-system
spec:
  addresses:
  - %s
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: default
  namespace: metallb-system
spec:
  ipAddressPools:
  - default-pool`, m.config.Network.MetalLBRange)

	if err := executor.WriteFile("/tmp/metallb-config.yaml", config); err != nil {
		return err
	}

	// Retry loop for applying config (webhook may not be ready)
	for i := 1; i <= 15; i++ {
		_, err := m.exec.RunShell("kubectl apply -f /tmp/metallb-config.yaml")
		if err == nil {
			fmt.Println("MetalLB configured successfully!")
			return nil
		}
		fmt.Printf("Attempt %d/15 failed, waiting for webhook... (retry in 10s)\n", i)
		time.Sleep(10 * time.Second)
	}

	return fmt.Errorf("failed to configure MetalLB after 15 attempts")
}

func (m *MetalLB) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := m.exec.RunShell("kubectl get pods -n metallb-system -l component=controller -o jsonpath='{.items[0].status.phase}'")
		if err == nil && out == "Running" {
			fmt.Println("MetalLB controller is ready!")
			return nil
		}
		fmt.Println("Waiting for MetalLB controller...")
		time.Sleep(10 * time.Second)
	}
	// Don't fail, continue with configuration
	fmt.Println("Warning: MetalLB controller may still be starting")
	return nil
}