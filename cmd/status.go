package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
	"github.com/techiescamp/k8s-provisioner/internal/version"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show cluster and node status",
	RunE: func(cmd *cobra.Command, args []string) error {
		exec := executor.New(IsVerbose())

		hostname, _ := os.Hostname()
		fmt.Printf("=== Node: %s ===\n\n", hostname)

		// Check CRI-O status
		fmt.Println("CRI-O Status:")
		if out, err := exec.Run("systemctl", "is-active", "crio"); err == nil {
			fmt.Printf("  Service: %s", out)
		} else {
			fmt.Println("  Service: not installed")
		}

		// Check kubelet status
		fmt.Println("\nKubelet Status:")
		if out, err := exec.Run("systemctl", "is-active", "kubelet"); err == nil {
			fmt.Printf("  Service: %s", out)
		} else {
			fmt.Println("  Service: not installed")
		}

		// Check kubectl version
		fmt.Println("\nKubernetes Version:")
		if out, err := exec.Run("kubectl", "version", "--client", "--short"); err == nil {
			fmt.Printf("  %s", out)
		} else {
			fmt.Println("  kubectl not installed")
		}

		// Check cluster nodes (only works on controlplane)
		fmt.Println("\nCluster Nodes:")
		if out, err := exec.Run("kubectl", "get", "nodes", "-o", "wide"); err == nil {
			for _, line := range strings.Split(out, "\n") {
				if line != "" {
					fmt.Printf("  %s\n", line)
				}
			}
		} else {
			fmt.Println("  Unable to get nodes (not controlplane or cluster not initialized)")
		}

		// Check pods in key namespaces
		fmt.Println("\nCluster Components:")
		namespaces := []string{"kube-system", "calico-system", "metallb-system", "istio-system"}
		for _, ns := range namespaces {
			out, err := exec.Run("kubectl", "get", "pods", "-n", ns, "--no-headers")
			if err == nil && out != "" {
				lines := strings.Split(strings.TrimSpace(out), "\n")
				running := 0
				for _, line := range lines {
					if strings.Contains(line, "Running") {
						running++
					}
				}
				fmt.Printf("  %s: %d/%d pods running\n", ns, running, len(lines))
			}
		}

		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show k8s-provisioner version",
	Run: func(cmd *cobra.Command, args []string) {
		info := version.Get()
		fmt.Println(info.String())

		fmt.Println("\nConfigured component versions:")
		cfg := GetConfig()
		if cfg != nil {
			fmt.Printf("  Kubernetes: %s\n", cfg.Versions.Kubernetes)
			fmt.Printf("  CRI-O: %s\n", cfg.Versions.CriO)
			fmt.Printf("  Calico: %s\n", cfg.Versions.Calico)
			fmt.Printf("  MetalLB: %s\n", cfg.Versions.MetalLB)
			fmt.Printf("  Istio: %s\n", cfg.Versions.Istio)
		}
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(versionCmd)
}