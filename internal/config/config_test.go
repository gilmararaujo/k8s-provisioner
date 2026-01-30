package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_ValidFile(t *testing.T) {
	cfg, err := Load("../../testdata/config_valid.yaml")

	require.NoError(t, err, "Load should not return error for valid file")
	require.NotNil(t, cfg, "Config should not be nil")

	// Verify cluster config
	assert.Equal(t, "test-cluster", cfg.Cluster.Name)
	assert.Equal(t, "10.244.0.0/16", cfg.Cluster.PodCIDR)
	assert.Equal(t, "10.96.0.0/12", cfg.Cluster.ServiceCIDR)

	// Verify versions
	assert.Equal(t, "v1.31.0", cfg.Versions.Kubernetes)
	assert.Equal(t, "v1.31.0", cfg.Versions.CriO)
	assert.Equal(t, "v3.28.0", cfg.Versions.Calico)

	// Verify network
	assert.Equal(t, "eth0", cfg.Network.Interface)
	assert.Equal(t, "192.168.56.10", cfg.Network.ControlPlaneIP)

	// Verify nodes count
	assert.Len(t, cfg.Nodes, 3, "Should have 3 nodes")

	// Verify components
	assert.Equal(t, "calico", cfg.Components.CNI)
	assert.Equal(t, "metallb", cfg.Components.LoadBalancer)
	assert.Equal(t, "istio", cfg.Components.ServiceMesh)
}

func TestLoad_InvalidFile(t *testing.T) {
	cfg, err := Load("../../testdata/config_invalid.yaml")

	assert.Error(t, err, "Load should return error for invalid YAML")
	assert.Nil(t, cfg, "Config should be nil on error")
}

func TestLoad_FileNotFound(t *testing.T) {
	cfg, err := Load("../../testdata/nonexistent.yaml")

	assert.Error(t, err, "Load should return error for nonexistent file")
	assert.Nil(t, cfg, "Config should be nil on error")
}

func TestGetControlPlane_Found(t *testing.T) {
	cfg := &Config{
		Nodes: []NodeConfig{
			{Name: "worker01", IP: "192.168.1.11", Role: "worker"},
			{Name: "master01", IP: "192.168.1.10", Role: "controlplane"},
			{Name: "worker02", IP: "192.168.1.12", Role: "worker"},
		},
	}

	cp := cfg.GetControlPlane()

	require.NotNil(t, cp, "GetControlPlane should return a node")
	assert.Equal(t, "master01", cp.Name)
	assert.Equal(t, "192.168.1.10", cp.IP)
	assert.Equal(t, "controlplane", cp.Role)
}

func TestGetControlPlane_NotFound(t *testing.T) {
	cfg := &Config{
		Nodes: []NodeConfig{
			{Name: "worker01", IP: "192.168.1.11", Role: "worker"},
			{Name: "worker02", IP: "192.168.1.12", Role: "worker"},
		},
	}

	cp := cfg.GetControlPlane()

	assert.Nil(t, cp, "GetControlPlane should return nil when no controlplane exists")
}

func TestGetWorkers_Multiple(t *testing.T) {
	cfg := &Config{
		Nodes: []NodeConfig{
			{Name: "master01", IP: "192.168.1.10", Role: "controlplane"},
			{Name: "worker01", IP: "192.168.1.11", Role: "worker"},
			{Name: "worker02", IP: "192.168.1.12", Role: "worker"},
			{Name: "worker03", IP: "192.168.1.13", Role: "worker"},
		},
	}

	workers := cfg.GetWorkers()

	assert.Len(t, workers, 3, "Should return 3 workers")

	workerNames := make([]string, len(workers))
	for i, w := range workers {
		workerNames[i] = w.Name
		assert.Equal(t, "worker", w.Role)
	}
	assert.Contains(t, workerNames, "worker01")
	assert.Contains(t, workerNames, "worker02")
	assert.Contains(t, workerNames, "worker03")
}

func TestGetWorkers_Empty(t *testing.T) {
	cfg := &Config{
		Nodes: []NodeConfig{
			{Name: "master01", IP: "192.168.1.10", Role: "controlplane"},
		},
	}

	workers := cfg.GetWorkers()

	assert.Empty(t, workers, "Should return empty slice when no workers")
}

func TestGetWorkers_NoNodes(t *testing.T) {
	cfg := &Config{
		Nodes: []NodeConfig{},
	}

	workers := cfg.GetWorkers()

	assert.Empty(t, workers, "Should return empty slice when no nodes")
}

// Validation tests

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			Name:        "test-cluster",
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{
			Kubernetes: "1.32",
			CriO:       "v1.32",
		},
		Network: NetworkConfig{
			Interface:      "eth1",
			ControlPlaneIP: "192.168.56.10",
			MetalLBRange:   "192.168.56.200-192.168.56.250",
		},
		Storage: StorageConfig{
			NFSServer: "storage",
			NFSPath:   "/exports/k8s-volumes",
		},
		Nodes: []NodeConfig{
			{Name: "controlplane", Role: "controlplane"},
			{Name: "worker01", Role: "worker"},
		},
	}

	err := cfg.Validate()
	assert.NoError(t, err)
}

func TestValidate_MissingClusterName(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{Kubernetes: "1.32", CriO: "v1.32"},
		Network:  NetworkConfig{Interface: "eth1", ControlPlaneIP: "192.168.56.10"},
		Storage:  StorageConfig{NFSPath: "/exports"},
		Nodes:    []NodeConfig{{Name: "cp", Role: "controlplane"}},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cluster.name is required")
}

func TestValidate_InvalidPodCIDR(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			Name:        "test",
			PodCIDR:     "invalid-cidr",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{Kubernetes: "1.32", CriO: "v1.32"},
		Network:  NetworkConfig{Interface: "eth1", ControlPlaneIP: "192.168.56.10"},
		Storage:  StorageConfig{NFSPath: "/exports"},
		Nodes:    []NodeConfig{{Name: "cp", Role: "controlplane"}},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pod_cidr")
	assert.Contains(t, err.Error(), "not a valid CIDR")
}

func TestValidate_InvalidControlPlaneIP(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			Name:        "test",
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{Kubernetes: "1.32", CriO: "v1.32"},
		Network:  NetworkConfig{Interface: "eth1", ControlPlaneIP: "not-an-ip"},
		Storage:  StorageConfig{NFSPath: "/exports"},
		Nodes:    []NodeConfig{{Name: "cp", Role: "controlplane"}},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "controlplane_ip")
	assert.Contains(t, err.Error(), "not a valid IP")
}

func TestValidate_InvalidMetalLBRange(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			Name:        "test",
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{Kubernetes: "1.32", CriO: "v1.32"},
		Network: NetworkConfig{
			Interface:      "eth1",
			ControlPlaneIP: "192.168.56.10",
			MetalLBRange:   "invalid-range",
		},
		Storage: StorageConfig{NFSPath: "/exports"},
		Nodes:   []NodeConfig{{Name: "cp", Role: "controlplane"}},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metallb_range")
}

func TestValidate_NoControlPlane(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			Name:        "test",
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{Kubernetes: "1.32", CriO: "v1.32"},
		Network:  NetworkConfig{Interface: "eth1", ControlPlaneIP: "192.168.56.10"},
		Storage:  StorageConfig{NFSPath: "/exports"},
		Nodes:    []NodeConfig{{Name: "worker01", Role: "worker"}},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "controlplane")
}

func TestValidate_InvalidNodeRole(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			Name:        "test",
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{Kubernetes: "1.32", CriO: "v1.32"},
		Network:  NetworkConfig{Interface: "eth1", ControlPlaneIP: "192.168.56.10"},
		Storage:  StorageConfig{NFSPath: "/exports"},
		Nodes: []NodeConfig{
			{Name: "cp", Role: "controlplane"},
			{Name: "node", Role: "invalid-role"},
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid-role")
	assert.Contains(t, err.Error(), "invalid")
}

func TestValidate_InvalidNodeIP(t *testing.T) {
	cfg := &Config{
		Cluster: ClusterConfig{
			Name:        "test",
			PodCIDR:     "10.244.0.0/16",
			ServiceCIDR: "10.96.0.0/12",
		},
		Versions: VersionsConfig{Kubernetes: "1.32", CriO: "v1.32"},
		Network:  NetworkConfig{Interface: "eth1", ControlPlaneIP: "192.168.56.10"},
		Storage:  StorageConfig{NFSPath: "/exports"},
		Nodes: []NodeConfig{
			{Name: "cp", IP: "bad-ip", Role: "controlplane"},
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nodes[0].ip")
	assert.Contains(t, err.Error(), "not a valid IP")
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := &Config{
		Cluster:  ClusterConfig{}, // missing all fields
		Versions: VersionsConfig{},
		Network:  NetworkConfig{},
		Storage:  StorageConfig{},
		Nodes:    []NodeConfig{},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	// Should contain multiple errors
	assert.Contains(t, err.Error(), "cluster.name")
	assert.Contains(t, err.Error(), "versions.kubernetes")
	assert.Contains(t, err.Error(), "network.interface")
}

// Helper function tests

func TestIsValidIP(t *testing.T) {
	tests := []struct {
		ip    string
		valid bool
	}{
		{"192.168.56.10", true},
		{"10.0.0.1", true},
		{"255.255.255.255", true},
		{"0.0.0.0", true},
		{"::1", true},                              // IPv6 localhost
		{"2001:db8::1", true},                      // IPv6
		{"invalid", false},
		{"192.168.56", false},
		{"192.168.56.256", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			result := isValidIP(tt.ip)
			assert.Equal(t, tt.valid, result, "IP: %s", tt.ip)
		})
	}
}

func TestIsValidCIDR(t *testing.T) {
	tests := []struct {
		cidr  string
		valid bool
	}{
		{"10.244.0.0/16", true},
		{"192.168.0.0/24", true},
		{"10.0.0.0/8", true},
		{"0.0.0.0/0", true},
		{"2001:db8::/32", true},  // IPv6 CIDR
		{"invalid", false},
		{"192.168.56.10", false}, // IP without mask
		{"192.168.56.0/33", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.cidr, func(t *testing.T) {
			result := isValidCIDR(tt.cidr)
			assert.Equal(t, tt.valid, result, "CIDR: %s", tt.cidr)
		})
	}
}

func TestValidateIPRange(t *testing.T) {
	tests := []struct {
		name    string
		ipRange string
		wantErr bool
	}{
		{"valid range", "192.168.56.200-192.168.56.250", false},
		{"single IP range", "10.0.0.1-10.0.0.1", false},
		{"missing dash", "192.168.56.200", true},
		{"invalid start IP", "invalid-192.168.56.250", true},
		{"invalid end IP", "192.168.56.200-invalid", true},
		{"empty string", "", true},
		{"too many dashes", "192.168.56.200-192.168.56.250-extra", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIPRange(tt.ipRange)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
