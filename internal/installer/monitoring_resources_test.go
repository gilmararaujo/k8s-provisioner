package installer

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestInstallResourceMonitoring_ScrapesCadvisorAndPinsUpstream(t *testing.T) {
	sh := &fakeShell{}
	cfg := &config.Config{}
	cfg.Versions.ResourceDashboard = "abc1234"

	require.NoError(t, NewMonitoring(cfg, sh).installResourceMonitoring())

	require.Len(t, sh.stdins, 1)
	assert.Contains(t, sh.stdins[0], "path: /metrics/cadvisor")
	assert.Contains(t, sh.stdins[0], "targetLabel: metrics_path", "the rules select metrics_path=\"/metrics/cadvisor\"")
	assert.NotContains(t, sh.stdins[0], "throttled", "throttling series must not be dropped")
	assert.True(t, strings.Contains(sh.calls[1], "/abc1234/manifests/prometheus-rule.yaml"), "pinned commit")
	assert.Contains(t, sh.calls[1], `"k8s-lab-resources"`, "applied under the lab's name")
	assert.Contains(t, sh.calls[1], "s/resource_review:/k8s_lab:/g", "series recorded under the lab's prefix")
	assert.Contains(t, sh.calls[2], "delete prometheusrule resource-review-mixin", "no duplicate recordings from the old name")
}

func TestResourceReviewURL_RejectsNonCommit(t *testing.T) {
	cfg := &config.Config{}
	cfg.Versions.ResourceDashboard = "main; rm -rf /"

	_, err := NewMonitoring(cfg, &fakeShell{}).resourceReviewURL("x")
	assert.ErrorContains(t, err, "not a commit SHA")
}

func TestKubeStateMetrics_HonorsObjectLabels(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(&config.Config{}, sh).installKubeStateMetrics())

	data, err := os.ReadFile("/tmp/kube-state-metrics.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(data), "honorLabels: true", "without it every KSM series is relabeled to the KSM pod")
}

func TestGrafana_KeepsBundledPluginsOnReadOnlyRoot(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(&config.Config{}, sh).installGrafana())

	data, err := os.ReadFile("/tmp/grafana.yaml")
	require.NoError(t, err)
	m := string(data)
	assert.Contains(t, m, "readOnlyRootFilesystem: true")
	assert.Contains(t, m, "name: GF_PLUGINS_PREINSTALL_AUTO_UPDATE\n          value: \"false\"",
		"auto-updating bundled plugins fails on a read-only root and unregisters Prometheus/Tempo")
	assert.Contains(t, m, "mountPath: /etc/grafana/dashboards")
}

func TestInstallDNSMonitoring_ScrapesClusterAndLabDNS(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(externalDNSConfig(), sh).installDNSMonitoring())

	require.Len(t, sh.stdins, 1)
	assert.Contains(t, sh.stdins[0], "k8s-app: kube-dns", "cluster CoreDNS")
	assert.Contains(t, sh.stdins[0], "kind: ServiceMonitor")
	assert.Contains(t, sh.stdins[0], "kind: ScrapeConfig")
	assert.Contains(t, sh.stdins[0], `targets: ["192.168.56.20:9153"]`)
	assert.Contains(t, sh.stdins[0], "jobName: lab-dns")
}

func TestCertManagerMonitoring_MatchesMetricsPortAndKeepsCertNamespace(t *testing.T) {
	require.NoError(t, NewMonitoring(&config.Config{}, &fakeShell{}).installCertManagerMonitoring())

	data, err := os.ReadFile("/tmp/cert-manager-monitoring.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(data), "port: http-metrics", "cert-manager v1.21 Service port name; a mismatch silently scrapes nothing")
	assert.Contains(t, string(data), "honorLabels: true")
}

func TestLabCA_OutlivesTheDefault90Days(t *testing.T) {
	sh := &fakeShell{outputs: map[string]string{"get secret lab-ca-secret": "lab-ca-secret"}}
	require.NoError(t, NewCertManager(&config.Config{}, sh).createIssuer())

	data, err := os.ReadFile("/tmp/cert-manager-issuer.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(data), "duration: 87600h", "renewing the CA would invalidate the trust users installed")
}

func TestInstallDNSMonitoring_ClusterDNSWithoutLabDNS(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(&config.Config{}, sh).installDNSMonitoring())

	require.Len(t, sh.stdins, 1)
	assert.Contains(t, sh.stdins[0], "k8s-app: kube-dns")
	assert.NotContains(t, sh.stdins[0], "ScrapeConfig", "no storage node, no lab DNS target")
}

func TestGrafana_DisplaysTheLabTimezone(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cluster.Timezone = "America/Sao_Paulo"
	require.NoError(t, NewMonitoring(cfg, &fakeShell{}).installGrafana())

	data, err := os.ReadFile("/tmp/grafana.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: GF_DATE_FORMATS_DEFAULT_TIMEZONE\n          value: \"America/Sao_Paulo\"")
	assert.Contains(t, string(data), "image: grafana/grafana:13.2.4", "Sprintf arguments still line up")
}
