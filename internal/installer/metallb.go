package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type MetalLB struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewMetalLB(cfg *config.Config, exec executor.CommandExecutor) *MetalLB {
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
	if err := m.waitForReady(DefaultReadyTimeout); err != nil {
		return err
	}

	// Wait for webhook to stabilize
	fmt.Println("Waiting for MetalLB webhook to stabilize...")
	time.Sleep(MetalLBConfigureDelay)

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

	// Wait for webhook to be ready
	fmt.Println("Waiting for MetalLB webhook to be ready...")
	for i := 1; i <= 30; i++ {
		_, err := m.exec.RunShell("kubectl wait --for=condition=Ready pods -l component=controller -n metallb-system --timeout=10s 2>/dev/null")
		if err == nil {
			break
		}
		fmt.Printf("Waiting for controller pod... (%d/30)\n", i)
		time.Sleep(5 * time.Second)
	}

	// Retry loop for applying config (webhook may not be ready)
	for i := 1; i <= 30; i++ {
		_, err := m.exec.RunShell("kubectl apply -f /tmp/metallb-config.yaml 2>/dev/null")
		if err == nil {
			fmt.Println("MetalLB configured successfully!")
			return nil
		}
		fmt.Printf("Attempt %d/30 failed, waiting for webhook... (retry in 10s)\n", i)
		time.Sleep(DefaultPollInterval)
	}

	return fmt.Errorf("failed to configure MetalLB after 30 attempts")
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
		time.Sleep(DefaultPollInterval)
	}
	// Don't fail, continue with configuration
	fmt.Println("Warning: MetalLB controller may still be starting")
	return nil
}