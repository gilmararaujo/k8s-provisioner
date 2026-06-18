package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestKarpor_BaseHelmArgs(t *testing.T) {
	cfg := &config.Config{}
	cfg.Versions.Karpor = "0.7.6"
	k := NewKarpor(cfg, &fakeShell{})

	a := k.baseHelmArgs()

	assert.Contains(t, a, "helm upgrade --install karpor")
	assert.Contains(t, a, "--version 0.7.6")
	assert.Contains(t, a, "storageClass=nfs-static")
}

func TestKarpor_AIHelmArgs_EmptyWhenDisabled(t *testing.T) {
	k := NewKarpor(&config.Config{}, &fakeShell{})

	assert.Equal(t, "", k.aiHelmArgs())
}

func TestKarpor_AIHelmArgs_OllamaMapsToOpenAIBackend(t *testing.T) {
	cfg := &config.Config{}
	cfg.KarporAI.Enabled = true
	cfg.KarporAI.Backend = "ollama"
	k := NewKarpor(cfg, &fakeShell{})

	a := k.aiHelmArgs()

	assert.Contains(t, a, "server.ai.backend=openai", "ollama maps to the chart's openai backend")
	assert.Contains(t, a, "/v1", "baseUrl must be OpenAI-compatible")
}
