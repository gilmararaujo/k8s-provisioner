package installer

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

func (m *Monitoring) createMonitoringGateways() error {
	grafana, prometheus, alertmanager := m.config.Host("grafana"), m.config.Host("prometheus"), m.config.Host("alertmanager")
	tlsSecret, err := ensureGatewayCert(m.exec, "monitoring", grafana, prometheus, alertmanager)
	if err != nil {
		return err
	}

	gateway := fmt.Sprintf(`apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: monitoring-gateway
  namespace: monitoring
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - "%[1]s"
    - "%[2]s"
    - "%[3]s"
    tls:
      httpsRedirect: true
  - port:
      number: 443
      name: https
      protocol: HTTPS
    tls:
      mode: SIMPLE
      credentialName: %[4]s
    hosts:
    - "%[1]s"
    - "%[2]s"
    - "%[3]s"
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: grafana
  namespace: monitoring
spec:
  hosts:
  - "%[1]s"
  gateways:
  - monitoring-gateway
  http:
  - route:
    - destination:
        host: grafana
        port:
          number: 3000
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: prometheus
  namespace: monitoring
spec:
  hosts:
  - "%[2]s"
  gateways:
  - monitoring-gateway
  http:
  - route:
    - destination:
        host: prometheus
        port:
          number: 9090
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: alertmanager
  namespace: monitoring
spec:
  hosts:
  - "%[3]s"
  gateways:
  - monitoring-gateway
  http:
  - route:
    - destination:
        host: alertmanager
        port:
          number: 9093`, grafana, prometheus, alertmanager, tlsSecret)

	if err := executor.WriteFile("/tmp/monitoring-gateway.yaml", gateway); err != nil {
		return err
	}

	_, err = m.exec.RunShell("kubectl apply -f /tmp/monitoring-gateway.yaml")
	return err
}

func (m *Monitoring) installIstioMonitoring() error {
	resources := `apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: istio-proxies
  namespace: monitoring
spec:
  namespaceSelector:
    any: true
  selector:
    matchExpressions:
    - key: istio-prometheus-ignore
      operator: DoesNotExist
  jobLabel: envoy-stats
  podMetricsEndpoints:
  - path: /stats/prometheus
    targetPort: 15090
    interval: 15s
    relabelings:
    - action: keep
      sourceLabels: [__meta_kubernetes_pod_container_name]
      regex: "istio-proxy"
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: istiod
  namespace: monitoring
spec:
  namespaceSelector:
    matchNames:
    - istio-system
  selector:
    matchLabels:
      app: istiod
  endpoints:
  - port: http-monitoring
    interval: 15s`

	if err := executor.WriteFile("/tmp/istio-monitoring.yaml", resources); err != nil {
		return err
	}
	_, err := m.exec.RunShell("kubectl apply -f /tmp/istio-monitoring.yaml")
	return err
}

// installCertManagerMonitoring creates the cert-manager ServiceMonitor and the
// certificate-expiry PrometheusRule. Both require the Prometheus Operator CRDs
// (monitoring.coreos.com/v1), so they must run from the monitoring step rather
// than from the cert-manager installer, which runs earlier in the order.
func (m *Monitoring) installCertManagerMonitoring() error {
	resources := `apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: cert-manager
  namespace: monitoring
  labels:
    release: prometheus-stack
spec:
  jobLabel: app
  selector:
    matchLabels:
      app: cert-manager
  namespaceSelector:
    matchNames:
    - cert-manager
  endpoints:
  - port: http-metrics  # cert-manager v1.21 name (was tcp-prometheus-servicemonitor)
    path: /metrics
    interval: 30s
    scrapeTimeout: 10s
    # Certificate metrics carry the Certificate's namespace; keep it instead of
    # the cert-manager pod's (the alerts below report it).
    honorLabels: true
---
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: cert-manager
  namespace: monitoring
  labels:
    release: prometheus-stack
spec:
  groups:
  - name: cert-manager
    rules:
    - alert: CertificateExpiringSoon
      expr: certmanager_certificate_expiration_timestamp_seconds - time() < 30 * 24 * 3600
      for: 1h
      labels:
        severity: warning
      annotations:
        summary: "Certificate expiring soon"
        description: "Certificate {{ $labels.name }} in namespace {{ $labels.namespace }} expires in less than 30 days."
    - alert: CertificateExpiryCritical
      expr: certmanager_certificate_expiration_timestamp_seconds - time() < 7 * 24 * 3600
      for: 1h
      labels:
        severity: critical
      annotations:
        summary: "Certificate expiring critically"
        description: "Certificate {{ $labels.name }} in namespace {{ $labels.namespace }} expires in less than 7 days."
    - alert: CertificateNotReady
      expr: certmanager_certificate_ready_status{condition="True"} != 1
      for: 10m
      labels:
        severity: critical
      annotations:
        summary: "Certificate not ready"
        description: "Certificate {{ $labels.name }} in namespace {{ $labels.namespace }} is not in the Ready state."`

	if err := executor.WriteFile("/tmp/cert-manager-monitoring.yaml", resources); err != nil {
		return err
	}
	_, err := m.exec.RunShell("kubectl apply -f /tmp/cert-manager-monitoring.yaml")
	return err
}
