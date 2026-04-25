package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type VPA struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewVPA(cfg *config.Config, exec executor.CommandExecutor) *VPA {
	return &VPA{config: cfg, exec: exec}
}

func (v *VPA) Install() error {
	fmt.Println("Installing VPA (Vertical Pod Autoscaler)...")

	if err := v.installHelm(); err != nil {
		return fmt.Errorf("helm installation failed: %w", err)
	}

	if _, err := v.exec.RunShell("helm repo add cowboysysop https://cowboysysop.github.io/charts 2>/dev/null || true"); err != nil {
		fmt.Printf("Warning: could not add cowboysysop Helm repo: %v\n", err)
	}
	if _, err := v.exec.RunShell("helm repo update cowboysysop"); err != nil {
		fmt.Printf("Warning: helm repo update failed: %v\n", err)
	}

	cmd := "helm upgrade --install vpa cowboysysop/vertical-pod-autoscaler" +
		" --namespace kube-system" +
		" --wait --timeout=3m"
	if _, err := v.exec.RunShell(cmd); err != nil {
		return fmt.Errorf("vpa helm install failed: %w", err)
	}

	fmt.Println("Waiting for VPA to be ready...")
	if err := v.waitForReady(3 * time.Minute); err != nil {
		return fmt.Errorf("vpa did not become ready: %w", err)
	}

	v.printAccessInfo()
	return nil
}

func (v *VPA) installHelm() error {
	if _, err := v.exec.RunShell("helm version 2>/dev/null"); err == nil {
		return nil
	}
	fmt.Println("Installing Helm...")
	_, err := v.exec.RunShell("curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash")
	return err
}

func (v *VPA) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := v.exec.RunShell(
			"kubectl get deployment vpa-vertical-pod-autoscaler-recommender -n kube-system -o jsonpath='{.status.readyReplicas}' 2>/dev/null",
		)
		if err == nil && strings.TrimSpace(out) == "1" {
			return nil
		}
		fmt.Println("Waiting for VPA recommender...")
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for VPA recommender")
}

func (v *VPA) printAccessInfo() {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   VPA instalado!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("\nComponentes: Recommender, Updater, Admission Controller")
	fmt.Println("\nExemplo — VerticalPodAutoscaler em modo Auto:")
	fmt.Println(`  apiVersion: autoscaling.k8s.io/v1
  kind: VerticalPodAutoscaler
  metadata:
    name: prometheus-vpa
    namespace: monitoring
  spec:
    targetRef:
      apiVersion: apps/v1
      kind: Deployment
      name: prometheus
    updatePolicy:
      updateMode: "Auto"   # Recreate, Initial ou Off (só recomendação)
    resourcePolicy:
      containerPolicies:
      - containerName: prometheus
        minAllowed:
          cpu: 100m
          memory: 128Mi
        maxAllowed:
          cpu: 2
          memory: 2Gi`)
	fmt.Println("\nPara ver recomendações:")
	fmt.Println("  kubectl get vpa -A")
	fmt.Println("  kubectl describe vpa <name> -n <namespace>")
	fmt.Println("\nAtenção: não use VPA e HPA/KEDA no mesmo deployment.")
	fmt.Println(strings.Repeat("=", 50))
}