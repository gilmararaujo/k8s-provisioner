package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func certProbeConfig() *config.Config {
	cfg := &config.Config{Nodes: []config.NodeConfig{
		{Name: "storage", Role: "storage", IP: "192.168.56.20"},
		{Name: "controlplane", Role: "controlplane", IP: "192.168.56.10"},
		{Name: "node01", Role: "worker", IP: "192.168.56.11"},
	}}
	cfg.Vault.Enabled = true
	cfg.Components.ServiceMesh = "istio"
	cfg.Components.Keycloak = "enabled"
	return cfg
}

func TestTLSProbeGroups_CoverWhatCertManagerDoesNot(t *testing.T) {
	groups := map[string][]string{}
	for _, g := range NewMonitoring(certProbeConfig(), &fakeShell{}).tlsProbeGroups() {
		groups[g.name] = g.targets
	}

	assert.Equal(t, []string{"192.168.56.10:6443"}, groups["apiserver"])
	assert.Equal(t, []string{"192.168.56.10:10250", "192.168.56.11:10250"}, groups["kubelet"], "every cluster node, not storage")
	assert.Equal(t, []string{"192.168.56.20:8200"}, groups["vault"])
	assert.Contains(t, groups["gateway"], "keycloak.lab.internal:443", "hostname targets so the probe sends SNI")
	assert.NotContains(t, groups["gateway"], "karpor.lab.internal:443", "Karpor is not enabled")
}

func TestTLSProbeGroups_SkipsDisabledComponents(t *testing.T) {
	cfg := certProbeConfig()
	cfg.Vault.Enabled = false
	cfg.Components.ServiceMesh = "none"

	for _, g := range NewMonitoring(cfg, &fakeShell{}).tlsProbeGroups() {
		assert.NotContains(t, []string{"vault", "gateway"}, g.name)
	}
}

func TestInstallCertificateProbes_DeploysBlackboxAndProbes(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(certProbeConfig(), sh).installCertificateProbes())

	require.Len(t, sh.stdins, 1)
	m := sh.stdins[0]
	assert.Contains(t, m, "quay.io/prometheus/blackbox-exporter:v0.29.1", "pinned default version")
	assert.Contains(t, m, `sidecar.istio.io/inject: "false"`)
	assert.Contains(t, m, "kind: Probe")
	assert.Contains(t, m, "jobName: blackbox")
	assert.Contains(t, m, `static: ["192.168.56.10:10250", "192.168.56.11:10250"]`)
}

func TestCertificatesSection_SelectsTheProbeJob(t *testing.T) {
	assert.Contains(t, string(certificatesSection), `job=\"`+blackboxJob+`\"`)
	assert.Contains(t, string(certificatesSection), "certmanager_certificate_expiration_timestamp_seconds")
	assert.Contains(t, string(certificatesSection), "kubelet_certificate_manager_client_ttl_seconds")
}
