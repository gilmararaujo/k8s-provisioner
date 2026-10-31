package installer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func sloNames(slos []slo) []string {
	names := make([]string, len(slos))
	for i, s := range slos {
		names[i] = s.name
	}
	return names
}

func TestDNSSLOs_FollowInstalledComponents(t *testing.T) {
	assert.Equal(t, []string{
		"coredns-cluster-availability", "coredns-cluster-latency",
		"coredns-lab-availability", "coredns-lab-latency",
		"external-dns-freshness", "external-dns-coverage",
	}, sloNames(NewMonitoring(externalDNSConfig(), &fakeShell{}).dnsSLOs()))

	assert.Equal(t, []string{"coredns-cluster-availability", "coredns-cluster-latency"},
		sloNames(NewMonitoring(&config.Config{}, &fakeShell{}).dnsSLOs()), "no storage node, no lab DNS SLOs")
}

func TestSLOPrometheusRule_RecordsEveryWindowAndAlertsOnBurnRate(t *testing.T) {
	m := NewMonitoring(externalDNSConfig(), &fakeShell{})
	out, err := sloPrometheusRule(m.dnsSLOs(), m.dnsJobs())
	require.NoError(t, err)

	var rule struct {
		Spec struct {
			Groups []promRuleGroup `yaml:"groups"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(out), &rule), "valid YAML despite PromQL quotes and braces")

	rules := map[string]promRule{}
	var records int
	for _, g := range rule.Spec.Groups {
		for _, r := range g.Rules {
			if r.Record != "" {
				records++
				assert.NotContains(t, r.Expr, "{w}", "window placeholder substituted")
			} else {
				rules[r.Alert+"/"+r.Labels["slo"]+r.Expr[:min(len(r.Expr), 30)]] = r
			}
		}
	}
	// 6 SLOs x (objective + 5 windows) + 2 time-based indicators
	assert.Equal(t, 6*6+2, records)

	var fast, slow, down int
	for _, r := range rules {
		switch r.Alert {
		case "SLOErrorBudgetBurnFast":
			fast++
			assert.Equal(t, "critical", r.Labels["severity"])
			if strings.HasPrefix(r.Labels["slo"], "coredns-") {
				assert.Contains(t, r.Expr, ">= 60", "event SLOs need traffic before alerting")
			} else {
				assert.NotContains(t, r.Expr, ">= 60", "time-based SLOs burn budget even with no traffic")
			}
			if strings.HasSuffix(r.Labels["slo"], "-availability") {
				assert.Contains(t, r.Expr, "> 0.0144", "14.4 x (1 - 0.999)")
			}
		case "SLOErrorBudgetBurnSlow":
			slow++
			assert.Equal(t, "warning", r.Labels["severity"])
		case "DNSScrapeTargetDown":
			down++
			assert.Contains(t, r.Expr, "or vector(0)", "a target with no pods has no up series at all")
			assert.NotEmpty(t, r.Labels["job"])
		}
	}
	assert.Equal(t, 6, fast)
	assert.Equal(t, 6, slow)
	assert.Equal(t, 3, down, "kube-dns, lab-dns, external-dns")
}

func TestTimeSLOs_CountAnOutageAsBad(t *testing.T) {
	for _, s := range NewMonitoring(externalDNSConfig(), &fakeShell{}).dnsSLOs() {
		if s.good != "" {
			assert.Contains(t, s.good, `or on() ((max(up{job="external-dns"}) or vector(0)) == 0`,
				"%s: a down ExternalDNS must record 0, not nothing; an up one without data yet records nothing", s.name)
			assert.Contains(t, s.good, `count_over_time(up{job="external-dns"}[7d])`, "%s: but only once it was ever scraped", s.name)
		}
	}
}

func TestExternalDNSCoverage_ToleratesSyncPropagation(t *testing.T) {
	for _, s := range NewMonitoring(externalDNSConfig(), &fakeShell{}).dnsSLOs() {
		if s.name == "external-dns-coverage" {
			assert.Contains(t, s.good, "min_over_time(", "a host unpublished for one 30s sync is not an error")
			assert.Contains(t, s.good, "[2m:30s]")
			return
		}
	}
	t.Fatal("external-dns-coverage SLO missing")
}
