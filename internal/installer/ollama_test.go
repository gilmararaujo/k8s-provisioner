package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestBuildDeploymentManifest_LocalHasPVCAndLargeResources(t *testing.T) {
	o := NewOllama(&config.Config{}, &fakeShell{})

	m := o.buildDeploymentManifest(false)

	assert.Contains(t, m, "claimName: ollama-data", "local model must mount the PVC")
	assert.Contains(t, m, "memory: 4Gi", "local model requests the larger memory")
	assert.NotContains(t, m, "OLLAMA_API_KEY", "no API key env when none configured")
}

func TestBuildDeploymentManifest_CloudHasNoPVCAndSmallResources(t *testing.T) {
	o := NewOllama(&config.Config{}, &fakeShell{})

	m := o.buildDeploymentManifest(true)

	assert.NotContains(t, m, "claimName: ollama-data", "cloud model needs no PVC")
	assert.Contains(t, m, "memory: 256Mi", "cloud model requests the smaller memory")
}

func TestBuildDeploymentManifest_InjectsAPIKeyEnvWhenConfigured(t *testing.T) {
	cfg := &config.Config{} // Vault disabled -> resolver returns the config key
	cfg.Ollama.APIKey = "olka_test"
	o := NewOllama(cfg, &fakeShell{})

	m := o.buildDeploymentManifest(true)

	assert.Contains(t, m, "name: OLLAMA_API_KEY")
	assert.Contains(t, m, "name: ollama-api-key", "env must reference the secret")
}
