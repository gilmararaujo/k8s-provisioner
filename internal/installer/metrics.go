package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type MetricsServer struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewMetricsServer(cfg *config.Config, exec executor.CommandExecutor) *MetricsServer {
	return &MetricsServer{config: cfg, exec: exec}
}

func (m *MetricsServer) Install() error {
	fmt.Println("Installing Metrics Server...")

	// Pin to a specific version to avoid GitHub redirect issues and ensure
	// compatibility with Kubernetes 1.32. v0.7.2 is validated against k8s 1.32.
	metricsServerURL := "https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.7.2/components.yaml"

	if _, err := m.exec.RunShell(fmt.Sprintf("curl -fsSL %s -o /tmp/metrics-server.yaml", metricsServerURL)); err != nil {
		return fmt.Errorf("failed to download metrics-server manifest: %w", err)
	}

	// Inject --kubelet-insecure-tls for lab environments (self-signed kubelet certs).
	// The pattern targets the args list in the metrics-server container spec.
	patchCmd := `sed -i '/- --metric-resolution=/i\        - --kubelet-insecure-tls' /tmp/metrics-server.yaml`
	if _, err := m.exec.RunShell(patchCmd); err != nil {
		return fmt.Errorf("failed to patch metrics-server manifest: %w", err)
	}

	// --validate=false is required because the APIService object in the manifest
	// triggers a client-side validation error in kubectl 1.32 even though it is valid.
	if _, err := m.exec.RunShell("kubectl apply --validate=false -f /tmp/metrics-server.yaml"); err != nil {
		return err
	}

	// Wait for metrics-server to be ready
	fmt.Println("Waiting for Metrics Server to be ready...")
	if err := m.waitForReady(ShortReadyTimeout); err != nil {
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
		time.Sleep(DefaultPollInterval)
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
