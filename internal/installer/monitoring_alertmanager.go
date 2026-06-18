package installer

import (
	"fmt"
)

func (m *Monitoring) resolveAlertmanagerConfig() string {
	return NewSecretResolver(m.config).Resolve("Alertmanager config", defaultAlertmanagerConfig, "alertmanager_config")
}

const defaultAlertmanagerConfig = `global:
      resolve_timeout: 5m
    route:
      group_by: [alertname, namespace]
      group_wait: 30s
      group_interval: 5m
      repeat_interval: 12h
      receiver: "null"
    receivers:
    - name: "null"
    inhibit_rules: []`

func (m *Monitoring) installAlertmanager() error {
	alertmanagerConfig := m.resolveAlertmanagerConfig()

	alertmanager := fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: alertmanager
  namespace: monitoring
automountServiceAccountToken: false
---
apiVersion: v1
kind: Secret
metadata:
  name: alertmanager-alertmanager
  namespace: monitoring
stringData:
  alertmanager.yaml: |
    %s
---
apiVersion: monitoring.coreos.com/v1
kind: Alertmanager
metadata:
  name: alertmanager
  namespace: monitoring
spec:
  replicas: 1
  serviceAccountName: alertmanager
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    runAsGroup: 65534
    fsGroup: 65534
  resources:
    requests:
      memory: 64Mi
      cpu: 50m
    limits:
      memory: 128Mi
      cpu: 100m
---
apiVersion: v1
kind: Service
metadata:
  name: alertmanager
  namespace: monitoring
  labels:
    app: alertmanager
spec:
  type: ClusterIP
  ports:
  - name: web
    port: 9093
    targetPort: 9093
  selector:
    alertmanager: alertmanager`, alertmanagerConfig)

	// The manifest embeds the Alertmanager config Secret (which may carry SMTP /
	// webhook credentials when resolved from Vault). Pipe via stdin so it never
	// lands on disk world-readable in /tmp.
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", alertmanager); err != nil {
		return err
	}

	// Update Prometheus to send alerts to Alertmanager
	_, err := m.exec.RunShell(`kubectl patch prometheus prometheus -n monitoring --type=merge -p '{"spec":{"alerting":{"alertmanagers":[{"namespace":"monitoring","name":"alertmanager-operated","port":"web"}]}}}'`)
	return err
}
