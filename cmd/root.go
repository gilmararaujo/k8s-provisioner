package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/techiescamp/k8s-provisioner/internal/config"
)

var (
	cfgFile string
	verbose bool
	dryRun  bool
	cfg     *config.Config
)

// Commands that don't require config
var noConfigCommands = map[string]bool{
	"version":   true,
	"vbox":      true,
	"promisc":   true,
	"status":    true,
	"list":      true,
	"help":      true,
	"vault":     true,
	"init-info": true,
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
		// Skip config loading for commands that don't need it. A missing file is
		// fine here, but a present-but-malformed config must still surface — leaving
		// cfg nil would otherwise nil-deref later (e.g. GetConfig consumers).
		if noConfigCommands[cmd.Name()] {
			c, err := config.Load(cfgFile)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("invalid config %s: %w", cfgFile, err)
			}
			cfg = c
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
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "preview commands without mutating the host")
}

func GetConfig() *config.Config {
	return cfg
}

func IsVerbose() bool {
	return verbose
}

func IsDryRun() bool {
	return dryRun
}
