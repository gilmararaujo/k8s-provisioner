package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestIngressIP_PrefersLiveLoadBalancerIP(t *testing.T) {
	k := NewKeycloak(&config.Config{}, &fakeShell{
		outputs: map[string]string{"istio-ingressgateway": "192.168.56.205"},
	})

	assert.Equal(t, "192.168.56.205", k.ingressIP())
}

func TestIngressIP_FallsBackToMetalLBRangeStart(t *testing.T) {
	cfg := &config.Config{}
	cfg.Network.MetalLBRange = "192.168.56.200-192.168.56.250"
	// fakeShell returns "" for the svc lookup -> fallback to the range start.
	k := NewKeycloak(cfg, &fakeShell{})

	assert.Equal(t, "192.168.56.200", k.ingressIP())
}

func TestIngressIP_EmptyWhenNoLBAndNoRange(t *testing.T) {
	k := NewKeycloak(&config.Config{}, &fakeShell{})

	assert.Equal(t, "", k.ingressIP())
}
