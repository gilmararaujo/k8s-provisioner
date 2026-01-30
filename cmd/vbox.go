package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// Lab VM display names
var vmDisplayNames = []string{"Storage", "Master", "Node01", "Node02"}

// getVBoxManagePath returns the VBoxManage path based on the OS
func getVBoxManagePath() string {
	switch runtime.GOOS {
	case "windows":
		// Common paths on Windows
		paths := []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Oracle", "VirtualBox", "VBoxManage.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Oracle", "VirtualBox", "VBoxManage.exe"),
			"VBoxManage.exe", // If in PATH
		}
		for _, p := range paths {
			if _, err := exec.LookPath(p); err == nil {
				return p
			}
		}
		return "VBoxManage.exe"
	case "linux":
		// On Linux it's usually in PATH or /usr/bin
		if path, err := exec.LookPath("VBoxManage"); err == nil {
			return path
		}
		return "/usr/bin/VBoxManage"
	default: // darwin (macOS)
		if path, err := exec.LookPath("VBoxManage"); err == nil {
			return path
		}
		return "/usr/local/bin/VBoxManage"
	}
}

var vboxCmd = &cobra.Command{
	Use:   "vbox",
	Short: "VirtualBox management commands",
	Long:  `Commands to manage VirtualBox VMs for the Kubernetes lab.`,
}

var promiscCmd = &cobra.Command{
	Use:   "promisc",
	Short: "Enable promiscuous mode on all VMs",
	Long: `Enable promiscuous mode on network interface 2 (eth1) for all lab VMs.
This is required for MetalLB L2 mode to work properly with VirtualBox.

Supported platforms: Windows, macOS, Linux`,
	RunE: func(cmd *cobra.Command, args []string) error {
		vboxManage := getVBoxManagePath()

		// Check if VBoxManage exists
		if _, err := exec.LookPath(vboxManage); err != nil {
			return fmt.Errorf("VBoxManage not found. Please ensure VirtualBox is installed and in your PATH")
		}

		fmt.Printf("Platform: %s\n", runtime.GOOS)
		fmt.Printf("VBoxManage: %s\n\n", vboxManage)
		fmt.Println("Enabling promiscuous mode on all VMs...")

		var errors []string
		for _, vmName := range vmDisplayNames {
			if err := enablePromiscMode(vboxManage, vmName); err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", vmName, err))
				fmt.Printf("  [X] %s - %v\n", vmName, err)
			} else {
				fmt.Printf("  [OK] %s - promiscuous mode enabled\n", vmName)
			}
		}

		if len(errors) > 0 {
			fmt.Printf("\nWarning: Some VMs failed (they may not be running)\n")
		} else {
			fmt.Println("\nAll VMs configured successfully!")
		}

		return nil
	},
}

var promiscStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show promiscuous mode status for all VMs",
	RunE: func(cmd *cobra.Command, args []string) error {
		vboxManage := getVBoxManagePath()

		// Check if VBoxManage exists
		if _, err := exec.LookPath(vboxManage); err != nil {
			return fmt.Errorf("VBoxManage not found. Please ensure VirtualBox is installed and in your PATH")
		}

		fmt.Printf("Platform: %s\n", runtime.GOOS)
		fmt.Printf("VBoxManage: %s\n\n", vboxManage)
		fmt.Println("Promiscuous mode status:")

		for _, vmName := range vmDisplayNames {
			status, err := getPromiscStatus(vboxManage, vmName)
			if err != nil {
				fmt.Printf("  %s: not found or not running\n", vmName)
			} else {
				fmt.Printf("  %s: %s\n", vmName, status)
			}
		}

		return nil
	},
}

var listVMsCmd = &cobra.Command{
	Use:   "list",
	Short: "List all VirtualBox VMs",
	RunE: func(cmd *cobra.Command, args []string) error {
		vboxManage := getVBoxManagePath()

		// Check if VBoxManage exists
		if _, err := exec.LookPath(vboxManage); err != nil {
			return fmt.Errorf("VBoxManage not found. Please ensure VirtualBox is installed and in your PATH")
		}

		fmt.Println("Running VMs:")
		runningCmd := exec.Command(vboxManage, "list", "runningvms")
		if output, err := runningCmd.Output(); err == nil {
			if len(output) > 0 {
				fmt.Println(string(output))
			} else {
				fmt.Println("  (none)")
			}
		}

		fmt.Println("\nAll VMs:")
		allCmd := exec.Command(vboxManage, "list", "vms")
		if output, err := allCmd.Output(); err == nil {
			fmt.Println(string(output))
		}

		return nil
	},
}

func enablePromiscMode(vboxManage, vmName string) error {
	cmd := exec.Command(vboxManage, "controlvm", vmName, "nicpromisc2", "allow-all")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v - %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func getPromiscStatus(vboxManage, vmName string) (string, error) {
	cmd := exec.Command(vboxManage, "showvminfo", vmName, "--machinereadable")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "nicpromisc2=") {
			value := strings.Trim(strings.TrimPrefix(line, "nicpromisc2="), "\"")
			return value, nil
		}
	}

	return "unknown", nil
}

func init() {
	vboxCmd.AddCommand(promiscCmd)
	vboxCmd.AddCommand(promiscStatusCmd)
	vboxCmd.AddCommand(listVMsCmd)
	rootCmd.AddCommand(vboxCmd)
}