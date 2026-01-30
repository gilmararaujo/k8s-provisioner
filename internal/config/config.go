package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Cluster    ClusterConfig    `yaml:"cluster"`
	Versions   VersionsConfig   `yaml:"versions"`
	Network    NetworkConfig    `yaml:"network"`
	Storage    StorageConfig    `yaml:"storage"`
	Nodes      []NodeConfig     `yaml:"nodes"`
	Components ComponentsConfig `yaml:"components"`
}

type ClusterConfig struct {
	Name        string `yaml:"name"`
	PodCIDR     string `yaml:"pod_cidr"`
	ServiceCIDR string `yaml:"service_cidr"`
}

type VersionsConfig struct {
	Kubernetes string `yaml:"kubernetes"`
	CriO       string `yaml:"crio"`
	Calico     string `yaml:"calico"`
	MetalLB    string `yaml:"metallb"`
	Istio      string `yaml:"istio"`
}

type NetworkConfig struct {
	Interface      string `yaml:"interface"`
	ControlPlaneIP string `yaml:"controlplane_ip"`
	MetalLBRange   string `yaml:"metallb_range"`
}

type StorageConfig struct {
	NFSServer string `yaml:"nfs_server"`
	NFSPath   string `yaml:"nfs_path"`
}

type NodeConfig struct {
	Name string `yaml:"name"`
	IP   string `yaml:"ip"`
	Role string `yaml:"role"`
}

type ComponentsConfig struct {
	CNI          string `yaml:"cni"`
	LoadBalancer string `yaml:"load_balancer"`
	ServiceMesh  string `yaml:"service_mesh"`
	Monitoring   string `yaml:"monitoring"`
	Logging      string `yaml:"logging"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// Validate checks all required fields and formats
func (c *Config) Validate() error {
	var errors []string

	// Cluster validation
	if c.Cluster.Name == "" {
		errors = append(errors, "cluster.name is required")
	}
	if c.Cluster.PodCIDR == "" {
		errors = append(errors, "cluster.pod_cidr is required")
	} else if !isValidCIDR(c.Cluster.PodCIDR) {
		errors = append(errors, fmt.Sprintf("cluster.pod_cidr '%s' is not a valid CIDR", c.Cluster.PodCIDR))
	}
	if c.Cluster.ServiceCIDR == "" {
		errors = append(errors, "cluster.service_cidr is required")
	} else if !isValidCIDR(c.Cluster.ServiceCIDR) {
		errors = append(errors, fmt.Sprintf("cluster.service_cidr '%s' is not a valid CIDR", c.Cluster.ServiceCIDR))
	}

	// Versions validation
	if c.Versions.Kubernetes == "" {
		errors = append(errors, "versions.kubernetes is required")
	}
	if c.Versions.CriO == "" {
		errors = append(errors, "versions.crio is required")
	}

	// Network validation
	if c.Network.Interface == "" {
		errors = append(errors, "network.interface is required")
	}
	if c.Network.ControlPlaneIP == "" {
		errors = append(errors, "network.controlplane_ip is required")
	} else if !isValidIP(c.Network.ControlPlaneIP) {
		errors = append(errors, fmt.Sprintf("network.controlplane_ip '%s' is not a valid IP address", c.Network.ControlPlaneIP))
	}
	if c.Network.MetalLBRange != "" {
		if err := validateIPRange(c.Network.MetalLBRange); err != nil {
			errors = append(errors, fmt.Sprintf("network.metallb_range: %v", err))
		}
	}

	// Storage validation
	if c.Storage.NFSPath == "" {
		errors = append(errors, "storage.nfs_path is required")
	}

	// Nodes validation
	if len(c.Nodes) == 0 {
		errors = append(errors, "at least one node must be defined")
	}

	hasControlPlane := false
	validRoles := map[string]bool{"storage": true, "controlplane": true, "worker": true}

	for i, node := range c.Nodes {
		if node.Name == "" {
			errors = append(errors, fmt.Sprintf("nodes[%d].name is required", i))
		}
		if node.Role == "" {
			errors = append(errors, fmt.Sprintf("nodes[%d].role is required", i))
		} else if !validRoles[node.Role] {
			errors = append(errors, fmt.Sprintf("nodes[%d].role '%s' is invalid (must be: storage, controlplane, or worker)", i, node.Role))
		}
		if node.Role == "controlplane" {
			hasControlPlane = true
		}
		if node.IP != "" && !isValidIP(node.IP) {
			errors = append(errors, fmt.Sprintf("nodes[%d].ip '%s' is not a valid IP address", i, node.IP))
		}
	}

	if !hasControlPlane {
		errors = append(errors, "at least one node with role 'controlplane' is required")
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}

	return nil
}

// isValidIP checks if the string is a valid IPv4 or IPv6 address
func isValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// isValidCIDR checks if the string is a valid CIDR notation
func isValidCIDR(cidr string) bool {
	_, _, err := net.ParseCIDR(cidr)
	return err == nil
}

// validateIPRange validates a MetalLB IP range format (e.g., "192.168.56.200-192.168.56.250")
func validateIPRange(ipRange string) error {
	parts := strings.Split(ipRange, "-")
	if len(parts) != 2 {
		return fmt.Errorf("'%s' must be in format 'startIP-endIP'", ipRange)
	}

	startIP := net.ParseIP(strings.TrimSpace(parts[0]))
	endIP := net.ParseIP(strings.TrimSpace(parts[1]))

	if startIP == nil {
		return fmt.Errorf("start IP '%s' is invalid", parts[0])
	}
	if endIP == nil {
		return fmt.Errorf("end IP '%s' is invalid", parts[1])
	}

	// Check that both are the same IP version
	startV4 := startIP.To4()
	endV4 := endIP.To4()
	if (startV4 == nil) != (endV4 == nil) {
		return fmt.Errorf("start and end IPs must be the same version (IPv4 or IPv6)")
	}

	return nil
}

func (c *Config) GetControlPlane() *NodeConfig {
	for _, node := range c.Nodes {
		if node.Role == "controlplane" {
			return &node
		}
	}
	return nil
}

func (c *Config) GetWorkers() []NodeConfig {
	var workers []NodeConfig
	for _, node := range c.Nodes {
		if node.Role == "worker" {
			workers = append(workers, node)
		}
	}
	return workers
}