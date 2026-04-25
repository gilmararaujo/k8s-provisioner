package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type KEDA struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewKEDA(cfg *config.Config, exec executor.CommandExecutor) *KEDA {
	return &KEDA{config: cfg, exec: exec}
}

func (k *KEDA) Install() error {
	fmt.Println("Installing KEDA (Kubernetes Event-Driven Autoscaling)...")

	if err := k.installHelm(); err != nil {
		return fmt.Errorf("helm installation failed: %w", err)
	}

	if _, err := k.exec.RunShell("helm repo add kedacore https://kedacore.github.io/charts 2>/dev/null || true"); err != nil {
		fmt.Printf("Warning: could not add kedacore Helm repo: %v\n", err)
	}
	if _, err := k.exec.RunShell("helm repo update kedacore"); err != nil {
		fmt.Printf("Warning: helm repo update failed: %v\n", err)
	}

	cmd := "helm upgrade --install keda kedacore/keda" +
		" --namespace keda --create-namespace" +
		" --wait --timeout=3m"
	if _, err := k.exec.RunShell(cmd); err != nil {
		return fmt.Errorf("keda helm install failed: %w", err)
	}

	fmt.Println("Waiting for KEDA to be ready...")
	if err := k.waitForReady(3 * time.Minute); err != nil {
		return fmt.Errorf("keda did not become ready: %w", err)
	}

	k.printAccessInfo()
	return nil
}

func (k *KEDA) installHelm() error {
	if _, err := k.exec.RunShell("helm version 2>/dev/null"); err == nil {
		return nil
	}
	fmt.Println("Installing Helm...")
	_, err := k.exec.RunShell("curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash")
	return err
}

func (k *KEDA) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := k.exec.RunShell(
			"kubectl get deployment keda-operator -n keda -o jsonpath='{.status.readyReplicas}' 2>/dev/null",
		)
		if err == nil && strings.TrimSpace(out) == "1" {
			return nil
		}
		fmt.Println("Waiting for KEDA operator...")
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for KEDA operator")
}

func (k *KEDA) printAccessInfo() {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   KEDA instalado!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("\nEscalers disponíveis: Prometheus, Kafka, Redis, RabbitMQ, HTTP, Cron e mais.")
	fmt.Println("\nExemplo — ScaledObject com Prometheus:")
	fmt.Println(`  apiVersion: keda.sh/v1alpha1
  kind: ScaledObject
  metadata:
    name: my-app-scaler
  spec:
    scaleTargetRef:
      name: my-app
    minReplicaCount: 0
    maxReplicaCount: 10
    triggers:
    - type: prometheus
      metadata:
        serverAddress: http://prometheus.monitoring.svc:9090
        metricName: http_requests_total
        threshold: "100"
        query: sum(rate(http_requests_total[2m]))`)
	fmt.Println("\nPara verificar:")
	fmt.Println("  kubectl get scaledobject -A")
	fmt.Println("  kubectl get hpa -A  # KEDA cria um HPA por baixo")
	fmt.Println(strings.Repeat("=", 50))
}