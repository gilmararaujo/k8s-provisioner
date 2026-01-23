package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type MetricsServer struct {
	config *config.Config
	exec   *executor.Executor
}

func NewMetricsServer(cfg *config.Config, exec *executor.Executor) *MetricsServer {
	return &MetricsServer{config: cfg, exec: exec}
}

func (m *MetricsServer) Install() error {
	fmt.Println("Installing Metrics Server...")

	// Install metrics-server from official manifest
	// Using --kubelet-insecure-tls for lab environments (self-signed certs)
	metricsServerURL := "https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml"

	// Download and patch for insecure TLS (required for lab environments)
	patchCmd := fmt.Sprintf(`curl -sL %s | sed 's/args:/args:\n        - --kubelet-insecure-tls/' | kubectl apply -f -`, metricsServerURL)

	if _, err := m.exec.RunShell(patchCmd); err != nil {
		return err
	}

	// Wait for metrics-server to be ready
	fmt.Println("Waiting for Metrics Server to be ready...")
	if err := m.waitForReady(3 * time.Minute); err != nil {
		return err
	}

	fmt.Println("Metrics Server installed successfully!")
	m.printAccessInfo()
	return nil
}

func (m *MetricsServer) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := m.exec.RunShell("kubectl get deployment metrics-server -n kube-system -o jsonpath='{.status.availableReplicas}' 2>/dev/null")
		if err == nil && out == "1" {
			return nil
		}
		fmt.Println("Waiting for Metrics Server deployment...")
		time.Sleep(10 * time.Second)
	}
	fmt.Println("Warning: Metrics Server may still be starting")
	return nil
}

func (m *MetricsServer) printAccessInfo() {
	fmt.Println("\n========================================")
	fmt.Println("Metrics Server Installed")
	fmt.Println("========================================")
	fmt.Println("\nUsage:")
	fmt.Println("  kubectl top nodes    # Node CPU/Memory")
	fmt.Println("  kubectl top pods     # Pod CPU/Memory")
	fmt.Println("  kubectl top pods -A  # All namespaces")
	fmt.Println("\nNote: Metrics may take 1-2 minutes to be available")
	fmt.Println("========================================")
}
