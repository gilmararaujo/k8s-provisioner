package installer

import (
	"fmt"
	"os"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Istio struct {
	config *config.Config
	exec   *executor.Executor
}

func NewIstio(cfg *config.Config, exec *executor.Executor) *Istio {
	return &Istio{config: cfg, exec: exec}
}

func (i *Istio) Install() error {
	version := i.config.Versions.Istio

	// Download istioctl
	fmt.Printf("Downloading Istio %s...\n", version)
	downloadCmd := fmt.Sprintf("curl -L https://istio.io/downloadIstio | ISTIO_VERSION=%s sh -", version)
	if err := i.exec.RunShellWithOutput(downloadCmd); err != nil {
		return err
	}

	// Get current directory
	pwd, err := os.Getwd()
	if err != nil {
		pwd = "/root"
	}

	// Copy istioctl to /usr/local/bin
	istioctlPath := fmt.Sprintf("%s/istio-%s/bin/istioctl", pwd, version)
	if _, err := i.exec.RunShell(fmt.Sprintf("cp %s /usr/local/bin/", istioctlPath)); err != nil {
		return err
	}

	// Install Istio with default profile
	fmt.Println("Installing Istio with default profile...")
	if err := i.exec.RunShellWithOutput("istioctl install --set profile=default -y"); err != nil {
		return err
	}

	// Wait for Istio to be ready
	fmt.Println("Waiting for Istio to be ready...")
	if err := i.waitForReady(5 * time.Minute); err != nil {
		return err
	}

	// Enable sidecar injection for default namespace
	fmt.Println("Enabling sidecar injection for default namespace...")
	if _, err := i.exec.RunShell("kubectl label namespace default istio-injection=enabled --overwrite"); err != nil {
		return err
	}

	fmt.Println("Istio installed successfully!")
	return nil
}

func (i *Istio) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := i.exec.RunShell("kubectl get pods -n istio-system -o jsonpath='{.items[*].status.phase}' 2>/dev/null")
		if err == nil && out != "" {
			// Check if all pods are Running
			allRunning := true
			for _, phase := range []byte(out) {
				if phase != 'R' && phase != ' ' {
					allRunning = false
					break
				}
			}
			if allRunning {
				fmt.Println("Istio is ready!")
				return nil
			}
		}
		fmt.Println("Waiting for Istio pods...")
		time.Sleep(15 * time.Second)
	}
	// Don't fail, just warn
	fmt.Println("Warning: Istio pods may still be starting")
	return nil
}