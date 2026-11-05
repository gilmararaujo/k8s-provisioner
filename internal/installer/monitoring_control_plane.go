package installer

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// Control plane scrape jobs (the job label each target gets).
const (
	apiServerJob         = "apiserver"
	controllerManagerJob = "kube-controller-manager"
	schedulerJob         = "kube-scheduler"
	etcdJob              = "kube-etcd"
)

// controlPlaneScrapes publishes the control plane to Prometheus. The static
// pods run on the host network with the labels kubeadm gives them, so a
// selector Service per component lists every control plane node, and each
// ServiceMonitor scrapes them all (one target per node).
//
// The apiserver serves a cluster-CA certificate and is verified. The controller
// manager and scheduler serve self-signed certificates kubeadm does not issue
// from the CA, so their scrape skips verification but still authenticates with
// the Prometheus ServiceAccount token, which they authorize for /metrics.
// Each scrape keeps only the series the dashboard and alerts read: the full
// apiserver and etcd metrics are tens of thousands of series. One
// ServiceMonitor covers the three kube-system Services: each endpoint matches
// the Services exposing its port name, and the job is the Service name.
const controlPlaneScrapes = `apiVersion: v1
kind: Service
metadata:
  name: kube-controller-manager
  namespace: kube-system
  labels:
    k8s-lab/scrape: control-plane
spec:
  clusterIP: None
  selector:
    component: kube-controller-manager
    tier: control-plane
  ports:
  - name: https-metrics
    port: 10257
    targetPort: 10257
---
apiVersion: v1
kind: Service
metadata:
  name: kube-scheduler
  namespace: kube-system
  labels:
    k8s-lab/scrape: control-plane
spec:
  clusterIP: None
  selector:
    component: kube-scheduler
    tier: control-plane
  ports:
  - name: https-metrics
    port: 10259
    targetPort: 10259
---
apiVersion: v1
kind: Service
metadata:
  name: kube-etcd
  namespace: kube-system
  labels:
    k8s-lab/scrape: control-plane
spec:
  clusterIP: None
  selector:
    component: etcd
    tier: control-plane
  ports:
  - name: http-metrics
    port: 2381
    targetPort: 2381
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: apiserver
  namespace: monitoring
spec:
  jobLabel: component
  namespaceSelector:
    matchNames: [default]
  selector:
    matchLabels:
      component: apiserver
      provider: kubernetes
  endpoints:
  - port: https
    scheme: https
    interval: 30s
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    tlsConfig:
      caFile: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
      serverName: kubernetes
    metricRelabelings:
    # Two latency buckets (1s and +Inf) are enough for "share of requests
    # slower than 1s"; the full histogram is most of the apiserver's series.
    # Prometheus 3 normalizes le="1" to "1.0" before relabeling.
    - sourceLabels: [__name__, le]
      regex: (apiserver_request_total|apiserver_request_duration_seconds_count|apiserver_current_inflight_requests|apiserver_flowcontrol_rejected_requests_total|process_resident_memory_bytes);.*|apiserver_request_duration_seconds_bucket;(1|1\.0|\+Inf)
      action: keep
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: control-plane
  namespace: monitoring
spec:
  namespaceSelector:
    matchNames: [kube-system]
  selector:
    matchLabels:
      k8s-lab/scrape: control-plane
  endpoints:
  - port: https-metrics
    scheme: https
    interval: 30s
    bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
    tlsConfig:
      insecureSkipVerify: true
    metricRelabelings:
    - sourceLabels: [__name__]
      regex: workqueue_depth|leader_election_master_status|scheduler_pending_pods|scheduler_schedule_attempts_total|process_resident_memory_bytes
      action: keep
  - port: http-metrics
    scheme: http
    interval: 30s
    metricRelabelings:
    - sourceLabels: [__name__]
      regex: etcd_server_has_leader|etcd_server_leader_changes_seen_total|etcd_server_proposals_failed_total|etcd_mvcc_db_total_size_in_bytes|etcd_disk_wal_fsync_duration_seconds_(bucket|count)|etcd_disk_backend_commit_duration_seconds_(bucket|count)|process_resident_memory_bytes
      action: keep
`

// controlPlaneDownAlert fires when a control plane job stops reporting; a job
// whose pods vanish from discovery has no up series at all, so absent is down.
func controlPlaneDownAlert(name, job, who string) promRule {
	return promRule{
		Alert: name, Expr: fmt.Sprintf(`(max(up{job=%q}) or vector(0)) == 0`, job), For: "5m",
		Labels: map[string]string{"severity": "critical", "job": job},
		Annotations: map[string]string{
			"summary":     who + " is not reporting metrics",
			"description": fmt.Sprintf("No %s target has answered a scrape for 5 minutes: the component is down or unreachable from Prometheus.", job),
		},
	}
}

// controlPlaneRules are the control plane alerts, shown in Alertmanager and
// Grafana (no notification receiver is configured by default).
func controlPlaneRules() (string, error) {
	rule := func(name, expr, forDuration, severity, summary, description string) promRule {
		return promRule{
			Alert: name, Expr: expr, For: forDuration,
			Labels:      map[string]string{"severity": severity},
			Annotations: map[string]string{"summary": summary, "description": description},
		}
	}
	group := promRuleGroup{Name: "k8s-lab-control-plane", Interval: "30s", Rules: []promRule{
		controlPlaneDownAlert("KubeAPIServerDown", apiServerJob, "The API server"),
		controlPlaneDownAlert("KubeControllerManagerDown", controllerManagerJob, "The controller manager"),
		controlPlaneDownAlert("KubeSchedulerDown", schedulerJob, "The scheduler"),
		controlPlaneDownAlert("EtcdDown", etcdJob, "etcd"),
		rule("KubeAPIErrorsHigh",
			fmt.Sprintf(`sum(rate(apiserver_request_total{job=%[1]q,code=~"5.."}[5m])) / sum(rate(apiserver_request_total{job=%[1]q}[5m])) > 0.05`, apiServerJob),
			"10m", "warning", "More than 5% of API requests fail with a server error",
			"5xx responses from the API server over the last 5 minutes; check its logs and etcd."),
		rule("KubeAPISlowRequests",
			fmt.Sprintf(`1 - sum(rate(apiserver_request_duration_seconds_bucket{job=%[1]q,le=~"1|1.0",verb!~"WATCH|CONNECT"}[5m])) / sum(rate(apiserver_request_duration_seconds_count{job=%[1]q,verb!~"WATCH|CONNECT"}[5m])) > 0.05`, apiServerJob),
			"10m", "warning", "More than 5% of API requests take longer than 1 second",
			"Long-running verbs (WATCH, CONNECT) excluded. Slow etcd disk or an overloaded API server are the usual causes."),
		rule("EtcdNoLeader",
			fmt.Sprintf(`min(etcd_server_has_leader{job=%q}) == 0`, etcdJob),
			"1m", "critical", "An etcd member has no leader",
			"Without a leader etcd accepts no writes, so the cluster cannot change state."),
		rule("EtcdSlowDisk",
			fmt.Sprintf(`histogram_quantile(0.99, sum by (instance, le) (rate(etcd_disk_wal_fsync_duration_seconds_bucket{job=%q}[5m]))) > 0.5`, etcdJob),
			"10m", "warning", "etcd write-ahead log fsync p99 is above 500ms",
			"etcd waits for the disk on every write; at this latency the API server slows down and leader elections can fail."),
		rule("EtcdProposalsFailing",
			fmt.Sprintf(`sum(increase(etcd_server_proposals_failed_total{job=%q}[15m])) > 0`, etcdJob),
			"5m", "warning", "etcd is failing to commit proposals",
			"Failed proposals usually mean a lost quorum or leader elections."),
		rule("KubeControlPlaneNoLeader",
			fmt.Sprintf(`sum by (job) (leader_election_master_status{job=~"%s|%s"}) == 0`, controllerManagerJob, schedulerJob),
			"5m", "critical", "No controller manager or scheduler instance holds leadership",
			"Without a leader nothing reconciles (controller manager) or schedules (scheduler)."),
		rule("KubePodsUnschedulable",
			fmt.Sprintf(`sum(scheduler_pending_pods{job=%q,queue="unschedulable"}) > 0`, schedulerJob),
			"15m", "warning", "Pods have been unschedulable for 15 minutes",
			"See the pods' events (kubectl get events --field-selector reason=FailedScheduling): usually not enough CPU or memory, or a node selector or taint no node satisfies."),
	}}
	spec, err := yaml.Marshal(map[string]any{
		"apiVersion": "monitoring.coreos.com/v1",
		"kind":       "PrometheusRule",
		"metadata":   map[string]string{"name": "k8s-lab-control-plane", "namespace": "monitoring"},
		"spec":       map[string]any{"groups": []promRuleGroup{group}},
	})
	return string(spec), err
}

// installControlPlaneMonitoring applies the control plane scrape targets and alerts.
func (m *Monitoring) installControlPlaneMonitoring() error {
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", controlPlaneScrapes); err != nil {
		return fmt.Errorf("apply control plane scrape targets: %w", err)
	}
	rules, err := controlPlaneRules()
	if err != nil {
		return err
	}
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", rules); err != nil {
		return fmt.Errorf("apply control plane alerts: %w", err)
	}
	return nil
}
