package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func tempoDatasourceNames(t *testing.T, logging string) (string, []string) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Components.Logging = logging
	manifest := NewTempo(cfg, &fakeShell{}).grafanaDatasources()

	var cm struct {
		Data map[string]string `yaml:"data"`
	}
	assert.NoError(t, yaml.Unmarshal([]byte(manifest), &cm))
	var ds struct {
		Datasources []struct {
			Name string `yaml:"name"`
		} `yaml:"datasources"`
	}
	assert.NoError(t, yaml.Unmarshal([]byte(cm.Data["datasources.yaml"]), &ds))

	var names []string
	for _, d := range ds.Datasources {
		names = append(names, d.Name)
	}
	return manifest, names
}

func TestTempoDatasources_WithLoki(t *testing.T) {
	manifest, names := tempoDatasourceNames(t, "loki")
	assert.Equal(t, []string{"Prometheus", "Loki", "Tempo"}, names)
	assert.Contains(t, manifest, "tracesToLogsV2")
}

func TestTempoDatasources_WithoutLoki(t *testing.T) {
	manifest, names := tempoDatasourceNames(t, "none")
	assert.Equal(t, []string{"Prometheus", "Tempo"}, names)
	assert.NotContains(t, manifest, "loki-uid", "no dangling Loki references")
}
