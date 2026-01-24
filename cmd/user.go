package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/techiescamp/k8s-provisioner/internal/user"
)

var (
	userGroups      []string
	userNamespace   string
	userClusterRole string
	userRole        string
	userExpiration  int
	userOutputDir   string
	userKubeconfig  string
)

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Manage Kubernetes users with certificate authentication",
	Long: `Create, delete, and list Kubernetes users with X.509 certificate-based authentication.

This command generates:
  - RSA private key
  - Certificate Signing Request (CSR)
  - Signed certificate (via Kubernetes CSR API)
  - Kubeconfig file for the user
  - RBAC bindings (optional)`,
}

var userCreateCmd = &cobra.Command{
	Use:   "create [username]",
	Short: "Create a new user with certificate authentication",
	Long: `Create a new Kubernetes user with X.509 certificate authentication.

Examples:
  # Create user with view access to entire cluster
  k8s-provisioner user create joao --cluster-role view

  # Create user with admin access to a specific namespace
  k8s-provisioner user create maria --namespace dev --role admin

  # Create user in a specific group
  k8s-provisioner user create pedro --group developers --cluster-role edit

  # Create user with custom expiration (default: 365 days)
  k8s-provisioner user create ana --cluster-role view --expiration 30`,
	Args: cobra.ExactArgs(1),
	RunE: runUserCreate,
}

var userDeleteCmd = &cobra.Command{
	Use:   "delete [username]",
	Short: "Delete a user and its RBAC bindings",
	Long: `Delete a Kubernetes user, including:
  - ClusterRoleBindings
  - RoleBindings
  - Local certificate files`,
	Args: cobra.ExactArgs(1),
	RunE: runUserDelete,
}

var userListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all users created by k8s-provisioner",
	RunE:  runUserList,
}

var userCreateRoleCmd = &cobra.Command{
	Use:   "create-role [name]",
	Short: "Create a developer role in a namespace",
	Long: `Create a Role with common developer permissions in a namespace.

Permissions include:
  - pods, deployments, services, configmaps, secrets
  - jobs, cronjobs
  - ingresses, networkpolicies
  - horizontalpodautoscalers

Example:
  k8s-provisioner user create-role developer --namespace dev`,
	Args: cobra.ExactArgs(1),
	RunE: runUserCreateRole,
}

func init() {
	rootCmd.AddCommand(userCmd)
	userCmd.AddCommand(userCreateCmd)
	userCmd.AddCommand(userDeleteCmd)
	userCmd.AddCommand(userListCmd)
	userCmd.AddCommand(userCreateRoleCmd)

	// Default paths
	homeDir, _ := os.UserHomeDir()
	defaultKubeconfig := filepath.Join(homeDir, ".kube", "config")
	defaultOutputDir := filepath.Join(homeDir, ".k8s-users")

	// Global flags for user command
	userCmd.PersistentFlags().StringVar(&userKubeconfig, "kubeconfig", defaultKubeconfig, "Path to kubeconfig file")
	userCmd.PersistentFlags().StringVar(&userOutputDir, "output-dir", defaultOutputDir, "Directory to store user certificates")

	// Flags for create command
	userCreateCmd.Flags().StringSliceVarP(&userGroups, "group", "g", []string{}, "Groups for the user (can be specified multiple times)")
	userCreateCmd.Flags().StringVarP(&userNamespace, "namespace", "n", "", "Namespace for RoleBinding")
	userCreateCmd.Flags().StringVar(&userClusterRole, "cluster-role", "", "ClusterRole to bind (e.g., view, edit, admin)")
	userCreateCmd.Flags().StringVar(&userRole, "role", "", "Role to bind (requires --namespace)")
	userCreateCmd.Flags().IntVar(&userExpiration, "expiration", 365, "Certificate expiration in days")

	// Flags for create-role command
	userCreateRoleCmd.Flags().StringVarP(&userNamespace, "namespace", "n", "default", "Namespace for the Role")
}

func runUserCreate(cmd *cobra.Command, args []string) error {
	username := args[0]

	// Validate flags
	if userRole != "" && userNamespace == "" {
		return fmt.Errorf("--namespace is required when using --role")
	}

	if userClusterRole == "" && userRole == "" {
		fmt.Println("Warning: No --cluster-role or --role specified. User will have no permissions.")
		fmt.Println("You can add permissions later using kubectl or this tool.")
		fmt.Println()
	}

	// Create manager
	manager, err := user.NewManager(userKubeconfig, userOutputDir)
	if err != nil {
		return fmt.Errorf("failed to create user manager: %w", err)
	}

	// Create user
	cfg := user.UserConfig{
		Username:    username,
		Groups:      userGroups,
		Namespace:   userNamespace,
		ClusterRole: userClusterRole,
		Role:        userRole,
		Expiration:  userExpiration,
	}

	return manager.CreateUser(cfg)
}

func runUserDelete(cmd *cobra.Command, args []string) error {
	username := args[0]

	manager, err := user.NewManager(userKubeconfig, userOutputDir)
	if err != nil {
		return fmt.Errorf("failed to create user manager: %w", err)
	}

	return manager.DeleteUser(username)
}

func runUserList(cmd *cobra.Command, args []string) error {
	manager, err := user.NewManager(userKubeconfig, userOutputDir)
	if err != nil {
		return fmt.Errorf("failed to create user manager: %w", err)
	}

	return manager.ListUsers()
}

func runUserCreateRole(cmd *cobra.Command, args []string) error {
	roleName := args[0]

	manager, err := user.NewManager(userKubeconfig, userOutputDir)
	if err != nil {
		return fmt.Errorf("failed to create user manager: %w", err)
	}

	rules := user.GetDefaultDeveloperRules()
	return manager.CreateRole(roleName, userNamespace, rules)
}

