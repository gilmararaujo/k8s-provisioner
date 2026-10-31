package installer

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// SLOs for the DNS path every lab URL depends on: CoreDNS in the cluster,
// CoreDNS on the storage node and ExternalDNS. Error ratios are recorded per
// window and alerted with multi-window burn rates (Google SRE Workbook, ch. 5):
//
//	fast: 1h and 5m above 14.4x the sustainable error rate → critical
//	slow: 6h and 30m above 6x                              → warning
//
// The SLO window is 7 days, not the usual 28: the lab is rebuilt often. Over 7
// days the fast alert means ~9% of the budget gone in an hour, the slow one ~21%
// in six hours.
const (
	sloWindow       = "7d"
	sloMinEvents    = 60 // queries per alert window below which a ratio is noise
	sloLatencyLimit = "0.128"
)

var sloWindows = []string{"5m", "30m", "1h", "6h", sloWindow}

// slo is one service level objective.
type slo struct {
	name      string // "slo" label
	service   string // "service" label
	objective float64
	summary   string
	// errorRatio is the PromQL bad/total ratio over a window ({w} placeholder).
	errorRatio string
	// events counts requests over a window ({w}); empty for time-based SLOs,
	// whose alerts need no minimum-traffic guard.
	events string
	// good, for time-based SLOs, is recorded every evaluation as 1 (good) or 0.
	good string
}

// eventSLO builds a request-based SLO from bad and total request rates.
func eventSLO(name, service, summary string, objective float64, bad, total, count string) slo {
	return slo{
		name: name, service: service, objective: objective, summary: summary,
		// No traffic is no errors; without the guard 0/0 would record NaN.
		errorRatio: fmt.Sprintf("((%s) / (%s) unless on() (%s) == 0) or on() vector(0)", bad, total, total),
		events:     count,
	}
}

// timeSLO builds an SLO over the share of time good evaluates to 1. good must
// return 0, not nothing, when the service is down, so an outage burns budget
// (see externalDNSGood).
func timeSLO(name, service, summary string, objective float64, good string) slo {
	return slo{
		name: name, service: service, objective: objective, summary: summary,
		errorRatio: fmt.Sprintf(`1 - avg_over_time(slo:good:ratio{slo=%q}[{w}])`, name),
		good:       good,
	}
}

// coreDNSSLOs are the availability and latency SLOs of one CoreDNS job.
func coreDNSSLOs(service, job, zoneFilter, who string) []slo {
	sel := fmt.Sprintf(`job=%q%s`, job, zoneFilter)
	responses := fmt.Sprintf("sum(rate(coredns_dns_responses_total{%s}[{w}]))", sel)
	requests := fmt.Sprintf("sum(rate(coredns_dns_request_duration_seconds_count{%s}[{w}]))", sel)
	count := fmt.Sprintf("sum(increase(coredns_dns_request_duration_seconds_count{%s}[{w}]))", sel)
	return []slo{
		eventSLO(service+"-availability", service, who+" answers without SERVFAIL/REFUSED", 0.999,
			fmt.Sprintf(`sum(rate(coredns_dns_responses_total{%s,rcode=~"SERVFAIL|REFUSED"}[{w}]))`, sel), responses, count),
		eventSLO(service+"-latency", service, who+" answers within 128ms", 0.99,
			fmt.Sprintf(`%s - sum(rate(coredns_dns_request_duration_seconds_bucket{%s,le=%q}[{w}]))`, requests, sel, sloLatencyLimit),
			requests, count),
	}
}

// dnsSLOs returns the SLOs for the DNS components this config installs.
func (m *Monitoring) dnsSLOs() []slo {
	slos := coreDNSSLOs("coredns-cluster", clusterDNSJob, "", "Cluster DNS")
	if m.config.StorageIP() != "" {
		// Lab zone only: forwarding to public resolvers is outside the lab's control.
		slos = append(slos, coreDNSSLOs("coredns-lab", labDNSJob, `,zone!="."`, "Lab DNS")...)
	}
	if m.config.ExternalDNSEnabled() {
		slos = append(slos,
			timeSLO("external-dns-freshness", "external-dns", "ExternalDNS synced records within the last 2 minutes", 0.99,
				externalDNSGood(fmt.Sprintf(`time() - max(external_dns_controller_last_sync_timestamp_seconds{job=%q}) < bool 120`, externalDNSJob))),
			// A new Gateway host is unpublished until the next sync (30s); only a
			// host still unpublished after 2 minutes (4 syncs) is an error.
			timeSLO("external-dns-coverage", "external-dns", "every host the cluster requests is published within 2 minutes", 0.99,
				externalDNSGood(fmt.Sprintf(`min_over_time((sum(external_dns_source_records{job=%[1]q}) - sum(external_dns_registry_records{job=%[1]q}))[2m:30s]) <= bool 0`, externalDNSJob))),
		)
	}
	return slos
}

// externalDNSGood wraps an ExternalDNS indicator: the indicator while
// ExternalDNS is up, 0 (bad) while it is down, and nothing at all before it was
// first scraped in the SLO window, so install-time startup is not an outage.
// While it is up but the indicator has no data yet (a subquery's first aligned
// step precedes the first scrape), nothing is recorded either.
func externalDNSGood(indicator string) string {
	return fmt.Sprintf(`((%[1]s) and on() max(up{job=%[2]q}) == 1) or on() ((max(up{job=%[2]q}) or vector(0)) == 0 and on() count(count_over_time(up{job=%[2]q}[%[3]s])) > 0)`,
		indicator, externalDNSJob, sloWindow)
}

// installSLOs applies the SLO recording and alerting rules.
func (m *Monitoring) installSLOs() error {
	rules, err := sloPrometheusRule(m.dnsSLOs(), m.dnsJobs())
	if err != nil {
		return err
	}
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", rules); err != nil {
		return fmt.Errorf("apply SLO rules: %w", err)
	}
	return nil
}

// dnsJobs are the scrape jobs the SLOs read; an outage of one stops its
// metrics, so it gets its own "down" alert.
func (m *Monitoring) dnsJobs() []string {
	jobs := []string{clusterDNSJob}
	if m.config.StorageIP() != "" {
		jobs = append(jobs, labDNSJob)
	}
	if m.config.ExternalDNSEnabled() {
		jobs = append(jobs, externalDNSJob)
	}
	return jobs
}

type promRule struct {
	Record      string            `yaml:"record,omitempty"`
	Alert       string            `yaml:"alert,omitempty"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

type promRuleGroup struct {
	Name     string     `yaml:"name"`
	Interval string     `yaml:"interval"`
	Rules    []promRule `yaml:"rules"`
}

// sloPrometheusRule renders the PrometheusRule. Rules are serialized with a
// YAML encoder so PromQL quotes and braces need no manual escaping.
func sloPrometheusRule(slos []slo, jobs []string) (string, error) {
	indicators := promRuleGroup{Name: "k8s-lab-slo-indicators", Interval: "30s"}
	ratios := promRuleGroup{Name: "k8s-lab-slo-ratios", Interval: "30s"}
	alerts := promRuleGroup{Name: "k8s-lab-slo-alerts", Interval: "30s"}

	for _, s := range slos {
		labels := map[string]string{"slo": s.name, "service": s.service}
		if s.good != "" {
			indicators.Rules = append(indicators.Rules, promRule{Record: "slo:good:ratio", Expr: s.good, Labels: labels})
		}
		ratios.Rules = append(ratios.Rules, promRule{
			Record: "slo:objective:ratio", Expr: fmt.Sprintf("vector(%g)", s.objective), Labels: labels})
		for _, w := range sloWindows {
			ratios.Rules = append(ratios.Rules, promRule{
				Record: "slo:error_ratio:ratio_rate" + w, Expr: strings.ReplaceAll(s.errorRatio, "{w}", w), Labels: labels})
		}
		alerts.Rules = append(alerts.Rules,
			burnRateAlert(s, "SLOErrorBudgetBurnFast", "1h", "5m", 14.4, "2m", "critical"),
			burnRateAlert(s, "SLOErrorBudgetBurnSlow", "6h", "30m", 6, "15m", "warning"))
	}
	for _, job := range jobs {
		// A failing target reports up == 0, but one with no pods (scaled to 0,
		// deleted) drops out of discovery and up disappears: treat absent as down.
		alerts.Rules = append(alerts.Rules, promRule{
			Alert: "DNSScrapeTargetDown", Expr: fmt.Sprintf(`(max(up{job=%q}) or vector(0)) == 0`, job), For: "5m",
			Labels: map[string]string{"severity": "warning", "job": job},
			Annotations: map[string]string{
				"summary":     job + " is not reporting metrics",
				"description": "Its SLOs read no traffic while it is down, so they cannot show this outage.",
			},
		})
	}

	groups := []promRuleGroup{ratios, alerts}
	if len(indicators.Rules) > 0 {
		groups = append([]promRuleGroup{indicators}, groups...)
	}
	spec, err := yaml.Marshal(map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]string{"name": "k8s-lab-slos", "namespace": "monitoring"},
		"spec":       map[string]any{"groups": groups},
	})
	return string(spec), err
}

// burnRateAlert fires when both windows consume the error budget faster than
// factor times the rate that would exhaust it exactly at the end of the window.
func burnRateAlert(s slo, name, long, short string, factor float64, forDuration, severity string) promRule {
	threshold := factor * (1 - s.objective)
	expr := fmt.Sprintf(`slo:error_ratio:ratio_rate%s{slo=%q} > %g and slo:error_ratio:ratio_rate%s{slo=%q} > %g`,
		long, s.name, threshold, short, s.name, threshold)
	if s.events != "" {
		expr += fmt.Sprintf(" and on() %s >= %d", strings.ReplaceAll(s.events, "{w}", long), sloMinEvents)
	}
	return promRule{
		Alert: name, Expr: expr, For: forDuration,
		Labels: map[string]string{"severity": severity, "slo": s.name, "service": s.service},
		Annotations: map[string]string{
			"summary": fmt.Sprintf("%s: error budget burning %gx too fast", s.name, factor),
			"description": fmt.Sprintf("SLO: %s, %g%% over %s. Errors over the last %s and %s exceed %gx the sustainable rate.",
				s.summary, s.objective*100, sloWindow, long, short, factor),
		},
	}
}
