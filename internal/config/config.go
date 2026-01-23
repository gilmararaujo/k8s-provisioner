package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Cluster    ClusterConfig    `yaml:"cluster"`
	Versions   VersionsConfig   `yaml:"versions"`
	Network    NetworkConfig    `yaml:"network"`
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

	return &cfg, nil
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