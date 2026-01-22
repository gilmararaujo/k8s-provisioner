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
