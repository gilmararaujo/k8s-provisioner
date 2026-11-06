package installer

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

type alloyWorkload struct {
	Kind string `yaml:"kind"`
	Spec struct {
		Replicas *int `yaml:"replicas"`
		Strategy struct {
			Type string `yaml:"type"`
		} `yaml:"strategy"`
		Template struct {
			Spec struct {
				Tolerations []map[string]any `yaml:"tolerations"`
			} `yaml:"spec"`
		} `yaml:"template"`
	} `yaml:"spec"`
}

func alloyWorkloads(t *testing.T) []alloyWorkload {
	t.Helper()
	cfg := &config.Config{}
	cfg.Versions.Alloy = "v1.21.1"
	manifest := NewLoki(cfg, &fakeShell{}).alloyManifest()

	var workloads []alloyWorkload
	dec := yaml.NewDecoder(bytes.NewBufferString(manifest))
	for {
		var w alloyWorkload
		err := dec.Decode(&w)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if w.Kind == "Deployment" || w.Kind == "DaemonSet" {
			workloads = append(workloads, w)
		}
	}
	return workloads
}

// A DaemonSet (or more than one replica) would tail every pod once per
// collector and ship duplicate log lines to Loki.
func TestAlloyManifest_SingleCollector(t *testing.T) {
	workloads := alloyWorkloads(t)
	require.Len(t, workloads, 1)

	w := workloads[0]
	assert.Equal(t, "Deployment", w.Kind)
	require.NotNil(t, w.Spec.Replicas)
	assert.Equal(t, 1, *w.Spec.Replicas)
	assert.Equal(t, "Recreate", w.Spec.Strategy.Type, "a rolling update would run two collectors at once")
	assert.Empty(t, w.Spec.Template.Spec.Tolerations, "the collector has no reason to run on control plane nodes")
}

func TestInstallAlloy_RemovesOldDaemonSetBeforeApply(t *testing.T) {
	cfg := &config.Config{}
	cfg.Versions.Alloy = "v1.21.1"
	shell := &fakeShell{}

	require.NoError(t, NewLoki(cfg, shell).installAlloy())
	require.Len(t, shell.calls, 2)
	assert.Contains(t, shell.calls[0], "delete daemonset alloy -n monitoring --ignore-not-found")
	assert.Contains(t, shell.calls[1], "kubectl apply -f /tmp/alloy.yaml")
}
