package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/techiescamp/k8s-provisioner/internal/provisioner"
)

var provisionCmd = &cobra.Command{
	Use:   "provision",
	Short: "Provision the Kubernetes node",
	Long:  `Provision the current node with Kubernetes components based on its role.`,
}

var provisionCommonCmd = &cobra.Command{
	Use:   "common",
	Short: "Install common components (CRI-O, kubeadm, kubelet, kubectl)",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("=== Installing common components ===")
		p := provisioner.New(GetConfig(), IsVerbose())
		return p.InstallCommon()
	},
}

var provisionControlPlaneCmd = &cobra.Command{
	Use:   "controlplane",
	Short: "Initialize the control plane node",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("=== Initializing control plane ===")
		p := provisioner.New(GetConfig(), IsVerbose())
		return p.InitControlPlane()
	},
}

var provisionWorkerCmd = &cobra.Command{
	Use:   "worker",
	Short: "Join this node as a worker",
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("=== Joining cluster as worker ===")
		p := provisioner.New(GetConfig(), IsVerbose())
		return p.JoinWorker()
	},
}

var provisionAllCmd = &cobra.Command{
	Use:   "all",
	Short: "Run full provisioning based on node role",
	RunE: func(cmd *cobra.Command, args []string) error {
		hostname, err := os.Hostname()
		if err != nil {
			return err
		}

		p := provisioner.New(GetConfig(), IsVerbose())

		// Install common components
		fmt.Println("=== Installing common components ===")
		if err := p.InstallCommon(); err != nil {
			return err
		}

		// Determine role based on hostname
		cfg := GetConfig()
		var role string
		for _, node := range cfg.Nodes {
			if node.Name == hostname {
				role = node.Role
				break
			}
		}

		if role == "" {
			return fmt.Errorf("hostname %s not found in config", hostname)
		}

		if role == "controlplane" {
			fmt.Println("=== Initializing control plane ===")
			return p.InitControlPlane()
		} else {
			fmt.Println("=== Joining cluster as worker ===")
			return p.JoinWorker()
		}
	},
}

func init() {
	rootCmd.AddCommand(provisionCmd)
	provisionCmd.AddCommand(provisionCommonCmd)
	provisionCmd.AddCommand(provisionControlPlaneCmd)
	provisionCmd.AddCommand(provisionWorkerCmd)
	provisionCmd.AddCommand(provisionAllCmd)
}