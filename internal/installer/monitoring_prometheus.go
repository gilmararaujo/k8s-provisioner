package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

func (m *Monitoring) installPrometheusOperator() error {
	// Using prometheus-operator bundle
	promOpVersion := m.config.Versions.PrometheusOperator
	if promOpVersion == "" {
		promOpVersion = "v0.90.1"
	}
	bundleURL := fmt.Sprintf("https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/%s/bundle.yaml", promOpVersion)

	// Download and modify to use monitoring namespace
	if _, err := m.exec.RunShell(fmt.Sprintf("curl -sL --connect-timeout 10 --max-time 300 %s | sed 's/namespace: default/namespace: monitoring/g' | kubectl apply --server-side -f -", bundleURL)); err != nil {
		return err
	}

	// Wait for operator to be ready
	for i := 0; i < 30; i++ {
		out, err := m.exec.RunShell("kubectl get pods -n monitoring -l app.kubernetes.io/name=prometheus-operator -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if err == nil && out == "Running" {
			return nil
		}
		time.Sleep(shortPollInterval)
	}

	return nil
}

func (m *Monitoring) installPrometheus() error {
	prometheus := `apiVersion: monitoring.coreos.com/v1
kind: Prometheus
metadata:
  name: prometheus
  namespace: monitoring
spec:
  replicas: 1
  serviceAccountName: prometheus
  serviceMonitorSelector: {}
  serviceMonitorNamespaceSelector: {}
  podMonitorSelector: {}
  podMonitorNamespaceSelector: {}
  ruleSelector: {}
  ruleNamespaceSelector: {}
  resources:
    requests:
      memory: 400Mi
  enableAdminAPI: true
  storage:
    volumeClaimTemplate:
      spec:
        storageClassName: nfs-storage
        accessModes: ["ReadWriteOnce"]
        resources:
          requests:
            storage: 10Gi
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: prometheus
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: prometheus
rules:
- apiGroups: [""]
  resources:
  - nodes
  - nodes/metrics
  - services
  - endpoints
  - pods
  verbs: ["get", "list", "watch"]
- apiGroups: [""]
  resources:
  - configmaps
  verbs: ["get"]
- apiGroups:
  - networking.k8s.io
  resources:
  - ingresses
  verbs: ["get", "list", "watch"]
- nonResourceURLs: ["/metrics"]
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: prometheus
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: prometheus
subjects:
- kind: ServiceAccount
  name: prometheus
  namespace: monitoring
---
apiVersion: v1
kind: Service
metadata:
  name: prometheus
  namespace: monitoring
spec:
  type: ClusterIP
  ports:
  - name: web
    port: 9090
    targetPort: web
  selector:
    prometheus: prometheus`

	if err := executor.WriteFile("/tmp/prometheus.yaml", prometheus); err != nil {
		return err
	}

	_, err := m.exec.RunShell("kubectl apply -f /tmp/prometheus.yaml")
	return err
}
