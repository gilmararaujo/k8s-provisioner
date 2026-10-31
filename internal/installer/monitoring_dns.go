package installer

import (
	"fmt"
	"strings"
)

// Prometheus job names of the DNS targets; the dashboard's DNS row selects on them.
const (
	clusterDNSJob  = "kube-dns" // CoreDNS in kube-system (Service label k8s-app)
	externalDNSJob = "external-dns"
	labDNSJob      = "lab-dns" // CoreDNS on the storage node
)

// installDNSMonitoring scrapes the cluster DNS (CoreDNS in kube-system, which
// answers pods) and both halves of the lab DNS: ExternalDNS in the cluster
// (records it owns, sync health) and CoreDNS on the storage node (queries,
// response codes, latency). The storage CoreDNS runs outside the cluster, so it
// is a static ScrapeConfig target instead of a ServiceMonitor.
func (m *Monitoring) installDNSMonitoring() error {
	docs := []string{`apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: coredns
  namespace: monitoring
spec:
  jobLabel: k8s-app
  namespaceSelector:
    matchNames: [kube-system]
  selector:
    matchLabels:
      k8s-app: kube-dns
  endpoints:
  - port: metrics
    interval: 30s`}
	if m.config.ExternalDNSEnabled() {
		docs = append(docs, fmt.Sprintf(`apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: external-dns
  namespace: monitoring
spec:
  jobLabel: app
  namespaceSelector:
    matchNames: [%s]
  selector:
    matchLabels:
      app: external-dns
  endpoints:
  - port: http-metrics
    interval: 30s`, externalDNSNamespace))
	}
	if ip := m.config.StorageIP(); ip != "" {
		docs = append(docs, fmt.Sprintf(`apiVersion: monitoring.coreos.com/v1alpha1
kind: ScrapeConfig
metadata:
  name: %[1]s
  namespace: monitoring
spec:
  jobName: %[1]s
  scrapeInterval: 30s
  staticConfigs:
  - targets: ["%[2]s:9153"]
    labels:
      node: storage`, labDNSJob, ip))
	}
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", strings.Join(docs, "\n---\n")); err != nil {
		return fmt.Errorf("apply DNS scrape targets: %w", err)
	}
	return nil
}
