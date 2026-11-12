package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func storageNodeConfig() *config.Config {
	return &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
}

func TestStorageRules(t *testing.T) {
	out, err := storageRules()
	require.NoError(t, err)
	var rule struct {
		Spec struct {
			Groups []promRuleGroup `yaml:"groups"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(out), &rule))
	require.Len(t, rule.Spec.Groups, 1)

	exprs := map[string]string{}
	for _, r := range rule.Spec.Groups[0].Rules {
		exprs[r.Alert] = r.Expr
		assert.NotEmpty(t, r.Labels["severity"], r.Alert)
		assert.NotEmpty(t, r.Annotations["summary"], r.Alert)
	}
	for _, alert := range []string{"StorageNodeDown", "StorageNodeDiskFillingUp", "StorageNodeDiskAlmostFull",
		"PersistentVolumeFillingUp", "PersistentVolumeErrors"} {
		assert.Contains(t, exprs, alert)
	}
	assert.Equal(t, `(max(up{job="storage-node"}) or vector(0)) == 0`, exprs["StorageNodeDown"], "absent counts as down")
	assert.Contains(t, exprs["PersistentVolumeFillingUp"], "kubelet_volume_stats_available_bytes",
		"PVC fill reads the kubelet volume stats the kubelet ServiceMonitor keeps")
	assert.Contains(t, exprs["StorageNodeDiskAlmostFull"], `mountpoint="/"`, "the export's free space is the root mount's")
}

func TestInstallStorageNodeMonitoring_AppliesScrapeAndRules(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(storageNodeConfig(), sh).installStorageNodeMonitoring())
	require.Len(t, sh.stdins, 2)
	assert.Contains(t, sh.stdins[0], "kind: ScrapeConfig")
	assert.Contains(t, sh.stdins[0], "jobName: storage-node")
	assert.Contains(t, sh.stdins[0], `"192.168.56.20:9100"`, "scrapes node_exporter on the storage node")
	assert.Contains(t, sh.stdins[1], "kind: PrometheusRule")
}

func TestInstallStorageNodeMonitoring_NoStorageNode(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(&config.Config{}, sh).installStorageNodeMonitoring())
	assert.Empty(t, sh.stdins, "no storage node: nothing backs the PVs to scrape")
}

func TestStorageSection_SelectsStorageMetrics(t *testing.T) {
	s := string(storageSection)
	assert.Contains(t, s, `job=\"`+storageNodeJob+`\"`, "panels select the scrape job installStorageNodeMonitoring creates")
	assert.Contains(t, s, "kubelet_volume_stats_available_bytes", "PVC usage reads the kubelet volume stats")
	assert.Contains(t, s, "kube_persistentvolume_status_phase", "PV status reads kube-state-metrics")
}
