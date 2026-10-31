package installer

import (
	"fmt"
	"regexp"
)

// defaultResourceDashboard pins the upstream of the Resources row and its
// recording rules (github.com/learnk8s/resource-review-mixin) by commit: it
// publishes no release tags.
const defaultResourceDashboard = "982c301aaeb251c07c75476922c80a8090b05e35"

var commitSHA = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// kubeletServiceMonitor scrapes cAdvisor on every kubelet through the
// kube-system/kubelet Service that the Prometheus Operator maintains
// (--kubelet-service). The job ("kubelet") and metrics_path labels match what
// kube-prometheus produces, which the upstream Resources rules select on.
//
// Kubelet serving certificates are self-signed per node by kubeadm, so there is
// no CA to verify against; the scrape still authenticates with the Prometheus
// ServiceAccount token (RBAC: nodes/metrics).
//
// metricRelabelings mirror kube-prometheus-stack's cAdvisor volume filters,
// except container_cpu_cfs_throttled_seconds_total, which the throttling panels
// need.
const kubeletServiceMonitor = `apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kubelet
  namespace: monitoring
spec:
  namespaceSelector:
    matchNames: [kube-system]
  selector:
    matchLabels:
      app.kubernetes.io/name: kubelet
  endpoints:
  - port: https-metrics
    scheme: https
    path: /metrics/cadvisor
    interval: 30s
    honorLabels: true
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    tlsConfig:
      insecureSkipVerify: true
    relabelings:
    - sourceLabels: [__metrics_path__]
      targetLabel: metrics_path
    metricRelabelings:
    - sourceLabels: [__name__]
      regex: container_cpu_(load_average_10s|system_seconds_total|user_seconds_total)
      action: drop
    - sourceLabels: [__name__]
      regex: container_fs_(io_current|io_time_seconds_total|io_time_weighted_seconds_total|reads_merged_total|sector_reads_total|sector_writes_total|writes_merged_total)
      action: drop
    - sourceLabels: [__name__]
      regex: container_memory_(mapped_file|swap)
      action: drop
    - sourceLabels: [__name__]
      regex: container_(file_descriptors|tasks_state|threads_max)
      action: drop
    - sourceLabels: [__name__, scope]
      regex: container_memory_failures_total;hierarchy
      action: drop
    - sourceLabels: [__name__, interface]
      regex: container_network_.*;(cali|cilium|cni|lxc|nodelocaldns|tunl).*
      action: drop
    - sourceLabels: [__name__]
      regex: container_spec.*
      action: drop
    # cgroup-level series with no pod (system slices) only add cardinality
    - sourceLabels: [id, pod]
      regex: .+;
      action: drop
  # The kubelet's own metrics, reduced to its client certificate's remaining
  # lifetime (Certificates row of the lab dashboard).
  - port: https-metrics
    scheme: https
    path: /metrics
    interval: 5m
    honorLabels: true
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    tlsConfig:
      insecureSkipVerify: true
    relabelings:
    - sourceLabels: [__metrics_path__]
      targetLabel: metrics_path
    metricRelabelings:
    - sourceLabels: [__name__]
      regex: kubelet_certificate_manager_.*
      action: keep`

// installResourceMonitoring adds the per-container CPU/memory evidence the lab
// dashboard is built on: cAdvisor metrics from every kubelet plus the
// upstream recording rules (pinned in versions.resource_dashboard).
func (m *Monitoring) installResourceMonitoring() error {
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", kubeletServiceMonitor); err != nil {
		return fmt.Errorf("apply kubelet ServiceMonitor: %w", err)
	}

	url, err := m.resourceReviewURL("manifests/prometheus-rule.yaml")
	if err != nil {
		return err
	}
	// Applied under the lab's name; the upstream-named rule from earlier
	// versions is removed so the same series are not recorded twice.
	if _, err := m.exec.RunShell(fmt.Sprintf(
		"curl -fsSL --connect-timeout 10 --max-time 120 %s | sed -e %s | kubectl apply -f -", url, resourceRulesRename)); err != nil {
		return fmt.Errorf("apply workload resource recording rules: %w", err)
	}
	if _, err := m.exec.RunShell("kubectl delete prometheusrule resource-review-mixin -n monitoring --ignore-not-found"); err != nil {
		return fmt.Errorf("remove the previously named resource rules: %w", err)
	}
	return nil
}

// Upstream records its series as resource_review:*; the lab records them as
// k8s_lab:*. The rules and the dashboard come from the same pinned commit and
// are both rewritten, so they always agree.
const (
	upstreamSeriesPrefix = "resource_review:"
	labSeriesPrefix      = "k8s_lab:"
)

// resourceRulesRename renames the upstream PrometheusRule, its group and its
// recorded series to the lab's naming (sed program, single-quoted for the shell).
const resourceRulesRename = `'s/"resource-review-mixin"/"k8s-lab-resources"/; s/"name": "resource-review"/"name": "k8s-lab-resources"/; ` +
	`s/` + upstreamSeriesPrefix + `/` + labSeriesPrefix + `/g'`

// resourceReviewURL returns the raw GitHub URL of file at the pinned upstream
// commit. The commit is validated because it is interpolated into a shell command.
func (m *Monitoring) resourceReviewURL(file string) (string, error) {
	rev := m.config.Versions.ResourceDashboard
	if rev == "" {
		rev = defaultResourceDashboard
	}
	if !commitSHA.MatchString(rev) {
		return "", fmt.Errorf("versions.resource_dashboard %q is not a commit SHA", rev)
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/learnk8s/resource-review-mixin/%s/%s", rev, file), nil
}
