package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Monitoring struct {
	config *config.Config
	exec   executor.ShellExecutor
}

func NewMonitoring(cfg *config.Config, exec executor.ShellExecutor) *Monitoring {
	return &Monitoring{config: cfg, exec: exec}
}

func (m *Monitoring) Install() error {
	fmt.Println("Installing Monitoring Stack (Prometheus + Grafana)...")

	// Create monitoring namespace with Istio sidecar injection
	ns := `apiVersion: v1
kind: Namespace
metadata:
  name: monitoring
  labels:
    istio-injection: enabled`
	if err := executor.WriteFile("/tmp/monitoring-ns.yaml", ns); err != nil {
		return err
	}
	if _, err := m.exec.RunShell("kubectl apply -f /tmp/monitoring-ns.yaml"); err != nil {
		return err
	}

	// Create NFS StorageClass and PVs
	fmt.Println("Creating NFS Storage resources...")
	if err := m.createNFSStorage(); err != nil {
		return err
	}

	// Install Prometheus Operator CRDs and Operator
	fmt.Println("Installing Prometheus Operator...")
	if err := m.installPrometheusOperator(); err != nil {
		return err
	}

	// Wait for CRDs to be established
	fmt.Println("Waiting for CRDs to be established...")
	time.Sleep(monitoringInitDelay)

	// Install Prometheus instance
	fmt.Println("Installing Prometheus...")
	if err := m.installPrometheus(); err != nil {
		return err
	}

	// Install Grafana
	fmt.Println("Installing Grafana...")
	if err := m.installGrafana(); err != nil {
		return err
	}

	// Install Node Exporter
	fmt.Println("Installing Node Exporter...")
	if err := m.installNodeExporter(); err != nil {
		return err
	}

	// Install kube-state-metrics
	fmt.Println("Installing kube-state-metrics...")
	if err := m.installKubeStateMetrics(); err != nil {
		return err
	}

	// Install Alertmanager
	fmt.Println("Installing Alertmanager...")
	if err := m.installAlertmanager(); err != nil {
		return err
	}

	// Wait for all components to be ready
	fmt.Println("Waiting for monitoring stack to be ready...")
	if err := m.waitForReady(defaultReadyTimeout); err != nil {
		return err
	}

	// Create cert-manager scrape target + alert rules. These live in the
	// monitoring namespace and need the Prometheus Operator CRDs installed above;
	// cert-manager itself is installed earlier in the workload order, so its
	// Service already exists by now.
	fmt.Println("Creating cert-manager ServiceMonitor + PrometheusRule...")
	if err := m.installCertManagerMonitoring(); err != nil {
		fmt.Printf("Warning: Failed to create cert-manager monitoring resources: %v\n", err)
	}

	// Create Istio Gateways and scrape configs if Istio is enabled
	if m.config.Components.ServiceMesh == "istio" {
		fmt.Println("Creating Istio Gateways for monitoring...")
		if err := m.createMonitoringGateways(); err != nil {
			fmt.Printf("Warning: Failed to create monitoring gateways: %v\n", err)
		}
		fmt.Println("Creating Istio scrape targets (PodMonitor + ServiceMonitor)...")
		if err := m.installIstioMonitoring(); err != nil {
			fmt.Printf("Warning: Failed to create Istio monitoring resources: %v\n", err)
		}
	}

	fmt.Println("Monitoring stack installed successfully!")
	m.printAccessInfo()
	return nil
}

func (m *Monitoring) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check Prometheus Operator
		out, _ := m.exec.RunShell("kubectl get pods -n monitoring -l app.kubernetes.io/name=prometheus-operator -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Prometheus Operator...")
			time.Sleep(defaultPollInterval)
			continue
		}

		// Check Grafana
		out, _ = m.exec.RunShell("kubectl get pods -n monitoring -l app=grafana -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Grafana...")
			time.Sleep(defaultPollInterval)
			continue
		}

		fmt.Println("Monitoring stack is ready!")
		return nil
	}

	fmt.Println("Warning: Some monitoring components may still be starting")
	return nil
}

func (m *Monitoring) printAccessInfo() {
	fmt.Println("\n========================================")
	fmt.Println("Monitoring Stack Access Information")
	fmt.Println("========================================")
	fmt.Println("\n1. Get Istio Ingress IP:")
	fmt.Println("   INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')")
	fmt.Println("\n2. Add to /etc/hosts:")
	fmt.Println("   echo \"$INGRESS_IP grafana.local prometheus.local alertmanager.local\" | sudo tee -a /etc/hosts")
	fmt.Println("\n3. Access:")
	fmt.Println("   - Grafana:      http://grafana.local")
	fmt.Println("   - Prometheus:   http://prometheus.local")
	fmt.Println("   - Alertmanager: http://alertmanager.local")
	fmt.Println("\nGrafana Credentials:")
	fmt.Println("  User: admin")
	if m.config.Vault.Enabled {
		fmt.Println("  Password: (stored in Vault)")
		fmt.Println("  Retrieve: k8s-provisioner vault get-secret k8s-provisioner/api-keys")
		fmt.Println("\nAlertmanager Config:")
		fmt.Println("  Config: (stored in Vault as 'alertmanager_config')")
		fmt.Println("  Store:  vault kv put secret/k8s-provisioner/api-keys alertmanager_config=@alertmanager.yaml")
	} else {
		fmt.Println("  Password: (random — shown above during install)")
		fmt.Println("\nAlertmanager Config:")
		fmt.Println("  Default receiver: null (no notifications)")
		fmt.Println("  To configure: kubectl edit secret alertmanager-alertmanager -n monitoring")
	}
	fmt.Println("========================================")
}
