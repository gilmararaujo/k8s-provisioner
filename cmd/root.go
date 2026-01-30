package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/techiescamp/k8s-provisioner/internal/config"
)

var (
	cfgFile string
	verbose bool
	cfg     *config.Config
)

// Commands that don't require config
var noConfigCommands = map[string]bool{
	"version": true,
	"vbox":    true,
	"promisc": true,
	"status":  true,
	"list":    true,
	"help":    true,
}

var rootCmd = &cobra.Command{
	Use:   "k8s-provisioner",
	Short: "Kubernetes cluster provisioner for lab environments",
	Long: `k8s-provisioner is a CLI tool to provision Kubernetes clusters
for learning and lab environments. It automates the installation of:

- CRI-O (Container Runtime)
- Kubernetes (kubeadm, kubelet, kubectl)
- Calico (CNI)
- MetalLB (LoadBalancer)
- Istio (Service Mesh)`,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Skip config loading for commands that don't need it
		if noConfigCommands[cmd.Name()] {
			// Try to load config, but don't fail if it doesn't exist
			cfg, _ = config.Load(cfgFile)
			return nil
		}

		var err error
		cfg, err = config.Load(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "/etc/k8s-provisioner/config.yaml", "config file path")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
}

func GetConfig() *config.Config {
	return cfg
}

func IsVerbose() bool {
	return verbose
}