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
	exec   executor.ShellExecutor
}

func NewVPA(cfg *config.Config, exec executor.ShellExecutor) *VPA {
	return &VPA{config: cfg, exec: exec}
}

func (v *VPA) Install() error {
	fmt.Println("Installing VPA (Vertical Pod Autoscaler)...")

	if err := v.installHelm(); err != nil {
		return fmt.Errorf("helm installation failed: %w", err)
	}

	if _, err := v.exec.RunShell("helm repo add autoscaler https://kubernetes.github.io/autoscaler 2>/dev/null || true"); err != nil {
		fmt.Printf("Warning: could not add autoscaler Helm repo: %v\n", err)
	}
	if _, err := v.exec.RunShell("helm repo update autoscaler"); err != nil {
		fmt.Printf("Warning: helm repo update failed: %v\n", err)
	}

	v.adoptExistingCRDs()

	chartVersion := v.config.Versions.VPA
	if chartVersion == "" {
		chartVersion = "0.15.0"
	}
	// Official chart; single replicas without PDBs suit the lab (a PDB on a
	// single replica would block node drains).
	cmd := "helm upgrade --install vpa autoscaler/vertical-pod-autoscaler" +
		" --namespace kube-system --version " + chartVersion +
		" --set admissionController.replicas=1 --set recommender.replicas=1 --set updater.replicas=1" +
		" --set admissionController.podDisruptionBudget.enabled=false" +
		" --set recommender.podDisruptionBudget.enabled=false" +
		" --set updater.podDisruptionBudget.enabled=false" +
		" --wait --timeout=3m"
	if _, err := v.exec.RunShell(cmd); err != nil {
		return fmt.Errorf("vpa helm install failed: %w", err)
	}

	fmt.Println("Waiting for VPA to be ready...")
	if err := v.waitForReady(shortReadyTimeout); err != nil {
		return fmt.Errorf("vpa did not become ready: %w", err)
	}

	v.printAccessInfo()
	return nil
}

// vpaCRDs are templated by the official chart, so Helm refuses to install over
// copies left behind by a previous chart (e.g. cowboysysop) unless they carry
// this release's ownership metadata.
var vpaCRDs = []string{
	"verticalpodautoscalers.autoscaling.k8s.io",
	"verticalpodautoscalercheckpoints.autoscaling.k8s.io",
}

// adoptExistingCRDs marks pre-existing VPA CRDs as owned by the "vpa" release.
// A no-op on a fresh cluster, where the CRDs do not exist yet.
func (v *VPA) adoptExistingCRDs() {
	for _, crd := range vpaCRDs {
		if _, err := v.exec.RunShell("kubectl get crd " + crd + " >/dev/null 2>&1"); err != nil {
			continue
		}
		cmd := fmt.Sprintf("kubectl label crd %[1]s app.kubernetes.io/managed-by=Helm --overwrite && "+
			"kubectl annotate crd %[1]s meta.helm.sh/release-name=vpa meta.helm.sh/release-namespace=kube-system --overwrite", crd)
		if _, err := v.exec.RunShell(cmd); err != nil {
			fmt.Printf("Warning: could not adopt CRD %s into the vpa release: %v\n", crd, err)
		}
	}
}

func (v *VPA) installHelm() error {
	if _, err := v.exec.RunShell("helm version 2>/dev/null"); err == nil {
		return nil
	}
	fmt.Println("Installing Helm...")
	_, err := v.exec.RunShell("curl -fsSL --connect-timeout 10 --max-time 300 https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash")
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
		time.Sleep(defaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for VPA recommender")
}

func (v *VPA) printAccessInfo() {
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   VPA installed!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("\nComponents: Recommender, Updater, Admission Controller")
	fmt.Println("\nExample — VerticalPodAutoscaler in Auto mode:")
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
      updateMode: "Auto"   # Recreate, Initial or Off (recommendation only)
    resourcePolicy:
      containerPolicies:
      - containerName: prometheus
        minAllowed:
          cpu: 100m
          memory: 128Mi
        maxAllowed:
          cpu: 2
          memory: 2Gi`)
	fmt.Println("\nTo view recommendations:")
	fmt.Println("  kubectl get vpa -A")
	fmt.Println("  kubectl describe vpa <name> -n <namespace>")
	fmt.Println("\nWarning: do not use VPA and HPA/KEDA on the same deployment.")
	fmt.Println(strings.Repeat("=", 50))
}
