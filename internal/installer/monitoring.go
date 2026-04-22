package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Monitoring struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewMonitoring(cfg *config.Config, exec executor.CommandExecutor) *Monitoring {
	return &Monitoring{config: cfg, exec: exec}
}

func (m *Monitoring) Install() error {
	fmt.Println("Installing Monitoring Stack (Prometheus + Grafana)...")

	// Create monitoring namespace
	if _, err := m.exec.RunShell("kubectl create namespace monitoring --dry-run=client -o yaml | kubectl apply -f -"); err != nil {
		return err
	}

	// Create NFS StorageClass and PVs
	fmt.Println("Creating NFS Storage resources...")
	if err := m.createNFSStorage(); err != nil {
		return err
	}

	// Install Prometheus Operator CRDs and Operator
	fmt.Println("Installing Prometheus Operator...")
	if err := m.installPrometheusOperator(); err != nil {
		return err
	}

	// Wait for CRDs to be established
	fmt.Println("Waiting for CRDs to be established...")
	time.Sleep(MonitoringInitDelay)

	// Install Prometheus instance
	fmt.Println("Installing Prometheus...")
	if err := m.installPrometheus(); err != nil {
		return err
	}

	// Install Grafana
	fmt.Println("Installing Grafana...")
	if err := m.installGrafana(); err != nil {
		return err
	}

	// Install Node Exporter
	fmt.Println("Installing Node Exporter...")
	if err := m.installNodeExporter(); err != nil {
		return err
	}

	// Install kube-state-metrics
	fmt.Println("Installing kube-state-metrics...")
	if err := m.installKubeStateMetrics(); err != nil {
		return err
	}

	// Install Alertmanager
	fmt.Println("Installing Alertmanager...")
	if err := m.installAlertmanager(); err != nil {
		return err
	}

	// Wait for all components to be ready
	fmt.Println("Waiting for monitoring stack to be ready...")
	if err := m.waitForReady(DefaultReadyTimeout); err != nil {
		return err
	}

	// Create Istio Gateways and scrape configs if Istio is enabled
	if m.config.Components.ServiceMesh == "istio" {
		fmt.Println("Creating Istio Gateways for monitoring...")
		if err := m.createMonitoringGateways(); err != nil {
			fmt.Printf("Warning: Failed to create monitoring gateways: %v\n", err)
		}
		fmt.Println("Creating Istio scrape targets (PodMonitor + ServiceMonitor)...")
		if err := m.installIstioMonitoring(); err != nil {
			fmt.Printf("Warning: Failed to create Istio monitoring resources: %v\n", err)
		}
	}

	fmt.Println("Monitoring stack installed successfully!")
	m.printAccessInfo()
	return nil
}

func (m *Monitoring) createNFSStorage() error {
	nfsServer := m.config.Storage.NFSServer
	if nfsServer == "" {
		nfsServer = "192.168.201.20" // default NFS server
	}
	nfsPath := m.config.Storage.NFSPath
	if nfsPath == "" {
		nfsPath = "/exports/k8s-volumes"
	}

	storage := fmt.Sprintf(`apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: nfs-storage
provisioner: kubernetes.io/no-provisioner
volumeBindingMode: Immediate
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: prometheus-pv
spec:
  capacity:
    storage: 10Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-storage
  nfs:
    server: %s
    path: %s/pv01
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: grafana-pv
spec:
  capacity:
    storage: 5Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-storage
  nfs:
    server: %s
    path: %s/pv02
---
apiVersion: v1
kind: PersistentVolume
metadata:
  name: loki-pv
spec:
  capacity:
    storage: 5Gi
  accessModes:
    - ReadWriteOnce
  persistentVolumeReclaimPolicy: Retain
  storageClassName: nfs-storage
  nfs:
    server: %s
    path: %s/pv03`, nfsServer, nfsPath, nfsServer, nfsPath, nfsServer, nfsPath)

	if err := executor.WriteFile("/tmp/nfs-storage.yaml", storage); err != nil {
		return err
	}

	_, err := m.exec.RunShell("kubectl apply -f /tmp/nfs-storage.yaml")
	return err
}

func (m *Monitoring) installPrometheusOperator() error {
	// Using prometheus-operator bundle
	bundleURL := "https://raw.githubusercontent.com/prometheus-operator/prometheus-operator/v0.90.1/bundle.yaml"

	// Download and modify to use monitoring namespace
	if _, err := m.exec.RunShell(fmt.Sprintf("curl -sL %s | sed 's/namespace: default/namespace: monitoring/g' | kubectl apply --server-side -f -", bundleURL)); err != nil {
		return err
	}

	// Wait for operator to be ready
	for i := 0; i < 30; i++ {
		out, err := m.exec.RunShell("kubectl get pods -n monitoring -l app.kubernetes.io/name=prometheus-operator -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if err == nil && out == "Running" {
			return nil
		}
		time.Sleep(ShortPollInterval)
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

func (m *Monitoring) installGrafana() error {
	password := m.resolveGrafanaPassword()

	// Cria K8s Secret com a senha (do Vault ou fallback)
	if err := m.createGrafanaSecret(password); err != nil {
		return err
	}

	grafana := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: grafana
  namespace: monitoring
automountServiceAccountToken: false
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-datasources
  namespace: monitoring
data:
  datasources.yaml: |
    apiVersion: 1
    datasources:
    - name: Prometheus
      type: prometheus
      access: proxy
      url: http://prometheus:9090
      isDefault: true
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: grafana
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: grafana
  template:
    metadata:
      labels:
        app: grafana
    spec:
      serviceAccountName: grafana
      securityContext:
        runAsNonRoot: true
        runAsUser: 472
        runAsGroup: 472
        fsGroup: 472
      containers:
      - name: grafana
        image: grafana/grafana:13.0.1
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        ports:
        - containerPort: 3000
        env:
        - name: GF_SECURITY_ADMIN_USER
          value: admin
        - name: GF_SECURITY_ADMIN_PASSWORD
          valueFrom:
            secretKeyRef:
              name: grafana-admin
              key: password
        - name: GF_USERS_ALLOW_SIGN_UP
          value: "false"
        volumeMounts:
        - name: datasources
          mountPath: /etc/grafana/provisioning/datasources
        - name: grafana-data
          mountPath: /var/lib/grafana
        - name: grafana-logs
          mountPath: /var/log/grafana
        - name: tmp
          mountPath: /tmp
        resources:
          requests:
            memory: 256Mi
            cpu: 100m
          limits:
            memory: 512Mi
            cpu: 500m
      volumes:
      - name: datasources
        configMap:
          name: grafana-datasources
      - name: grafana-data
        emptyDir: {}
      - name: grafana-logs
        emptyDir: {}
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: grafana
  namespace: monitoring
spec:
  type: ClusterIP
  ports:
  - port: 3000
    targetPort: 3000
  selector:
    app: grafana`

	if err := executor.WriteFile("/tmp/grafana.yaml", grafana); err != nil {
		return err
	}

	_, err := m.exec.RunShell("kubectl apply -f /tmp/grafana.yaml")
	return err
}

// resolveGrafanaPassword busca a senha do Vault se habilitado, fallback para "admin123".
func (m *Monitoring) resolveGrafanaPassword() string {
	if m.config.Vault.Enabled() {
		if val, err := FetchSecret(m.config.Vault.Addr, m.config.Vault.Token, "grafana_admin_password"); err == nil && val != "" {
			fmt.Println("Grafana password loaded from Vault")
			return val
		}
	}
	return "admin123"
}

func (m *Monitoring) createGrafanaSecret(password string) error {
	_, _ = m.exec.RunShell("kubectl delete secret grafana-admin -n monitoring 2>/dev/null || true")
	cmd := fmt.Sprintf(
		"kubectl create secret generic grafana-admin -n monitoring --from-literal=password=%s",
		password,
	)
	if _, err := m.exec.RunShell(cmd); err != nil {
		return fmt.Errorf("failed to create grafana-admin secret: %w", err)
	}
	fmt.Println("Grafana admin secret created from Vault")
	return nil
}

func (m *Monitoring) installNodeExporter() error {
	nodeExporter := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: node-exporter
  namespace: monitoring
automountServiceAccountToken: false
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: node-exporter
  namespace: monitoring
  labels:
    app: node-exporter
spec:
  selector:
    matchLabels:
      app: node-exporter
  template:
    metadata:
      labels:
        app: node-exporter
    spec:
      serviceAccountName: node-exporter
      hostNetwork: true
      hostPID: true
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        runAsGroup: 65534
      containers:
      - name: node-exporter
        image: prom/node-exporter:v1.11.1
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        args:
        - --path.procfs=/host/proc
        - --path.sysfs=/host/sys
        - --path.rootfs=/host/root
        ports:
        - containerPort: 9100
          hostPort: 9100
        volumeMounts:
        - name: proc
          mountPath: /host/proc
          readOnly: true
        - name: sys
          mountPath: /host/sys
          readOnly: true
        - name: root
          mountPath: /host/root
          readOnly: true
        resources:
          requests:
            memory: 64Mi
            cpu: 50m
          limits:
            memory: 128Mi
            cpu: 100m
      tolerations:
      - effect: NoSchedule
        operator: Exists
      volumes:
      - name: proc
        hostPath:
          path: /proc
      - name: sys
        hostPath:
          path: /sys
      - name: root
        hostPath:
          path: /
---
apiVersion: v1
kind: Service
metadata:
  name: node-exporter
  namespace: monitoring
  labels:
    app: node-exporter
spec:
  clusterIP: None
  ports:
  - name: metrics
    port: 9100
    targetPort: 9100
  selector:
    app: node-exporter
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: node-exporter
  namespace: monitoring
  labels:
    team: frontend
spec:
  selector:
    matchLabels:
      app: node-exporter
  endpoints:
  - port: metrics
    interval: 30s`

	if err := executor.WriteFile("/tmp/node-exporter.yaml", nodeExporter); err != nil {
		return err
	}

	_, err := m.exec.RunShell("kubectl apply -f /tmp/node-exporter.yaml")
	return err
}

func (m *Monitoring) installKubeStateMetrics() error {
	ksm := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: kube-state-metrics
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kube-state-metrics
rules:
- apiGroups: [""]
  resources:
  - configmaps
  - secrets
  - nodes
  - pods
  - services
  - resourcequotas
  - replicationcontrollers
  - limitranges
  - persistentvolumeclaims
  - persistentvolumes
  - namespaces
  - endpoints
  verbs: ["list", "watch"]
- apiGroups: ["apps"]
  resources:
  - statefulsets
  - daemonsets
  - deployments
  - replicasets
  verbs: ["list", "watch"]
- apiGroups: ["batch"]
  resources:
  - cronjobs
  - jobs
  verbs: ["list", "watch"]
- apiGroups: ["autoscaling"]
  resources:
  - horizontalpodautoscalers
  verbs: ["list", "watch"]
- apiGroups: ["networking.k8s.io"]
  resources:
  - ingresses
  verbs: ["list", "watch"]
- apiGroups: ["storage.k8s.io"]
  resources:
  - storageclasses
  - volumeattachments
  verbs: ["list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kube-state-metrics
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kube-state-metrics
subjects:
- kind: ServiceAccount
  name: kube-state-metrics
  namespace: monitoring
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kube-state-metrics
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kube-state-metrics
  template:
    metadata:
      labels:
        app: kube-state-metrics
    spec:
      serviceAccountName: kube-state-metrics
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        runAsGroup: 65534
        fsGroup: 65534
      containers:
      - name: kube-state-metrics
        image: registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.18.0
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        ports:
        - containerPort: 8080
          name: http-metrics
        - containerPort: 8081
          name: telemetry
        volumeMounts:
        - name: tmp
          mountPath: /tmp
        resources:
          requests:
            memory: 64Mi
            cpu: 50m
          limits:
            memory: 256Mi
            cpu: 200m
      volumes:
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: kube-state-metrics
  namespace: monitoring
  labels:
    app: kube-state-metrics
spec:
  ports:
  - name: http-metrics
    port: 8080
    targetPort: http-metrics
  - name: telemetry
    port: 8081
    targetPort: telemetry
  selector:
    app: kube-state-metrics
---
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  name: kube-state-metrics
  namespace: monitoring
  labels:
    team: frontend
spec:
  selector:
    matchLabels:
      app: kube-state-metrics
  endpoints:
  - port: http-metrics
    interval: 30s`

	if err := executor.WriteFile("/tmp/kube-state-metrics.yaml", ksm); err != nil {
		return err
	}

	_, err := m.exec.RunShell("kubectl apply -f /tmp/kube-state-metrics.yaml")
	return err
}

func (m *Monitoring) resolveAlertmanagerConfig() string {
	if m.config.Vault.Enabled() {
		if val, err := FetchSecret(m.config.Vault.Addr, m.config.Vault.Token, "alertmanager_config"); err == nil && val != "" {
			fmt.Println("Alertmanager config loaded from Vault")
			return val
		}
	}
	return `global:
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
}

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

	if err := executor.WriteFile("/tmp/alertmanager.yaml", alertmanager); err != nil {
		return err
	}

	if _, err := m.exec.RunShell("kubectl apply -f /tmp/alertmanager.yaml"); err != nil {
		return err
	}

	// Update Prometheus to send alerts to Alertmanager
	_, err := m.exec.RunShell(`kubectl patch prometheus prometheus -n monitoring --type=merge -p '{"spec":{"alerting":{"alertmanagers":[{"namespace":"monitoring","name":"alertmanager-operated","port":"web"}]}}}'`)
	return err
}

func (m *Monitoring) createMonitoringGateways() error {
	gateway := `apiVersion: networking.istio.io/v1
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
    - "grafana.local"
    - "prometheus.local"
    - "alertmanager.local"
    tls:
      httpsRedirect: true
  - port:
      number: 443
      name: https
      protocol: HTTPS
    tls:
      mode: SIMPLE
      credentialName: lab-tls-secret
    hosts:
    - "grafana.local"
    - "prometheus.local"
    - "alertmanager.local"
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: grafana
  namespace: monitoring
spec:
  hosts:
  - "grafana.local"
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
  - "prometheus.local"
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
  - "alertmanager.local"
  gateways:
  - monitoring-gateway
  http:
  - route:
    - destination:
        host: alertmanager
        port:
          number: 9093`

	if err := executor.WriteFile("/tmp/monitoring-gateway.yaml", gateway); err != nil {
		return err
	}

	_, err := m.exec.RunShell("kubectl apply -f /tmp/monitoring-gateway.yaml")
	return err
}

func (m *Monitoring) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check Prometheus Operator
		out, _ := m.exec.RunShell("kubectl get pods -n monitoring -l app.kubernetes.io/name=prometheus-operator -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Prometheus Operator...")
			time.Sleep(DefaultPollInterval)
			continue
		}

		// Check Grafana
		out, _ = m.exec.RunShell("kubectl get pods -n monitoring -l app=grafana -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Grafana...")
			time.Sleep(DefaultPollInterval)
			continue
		}

		fmt.Println("Monitoring stack is ready!")
		return nil
	}

	fmt.Println("Warning: Some monitoring components may still be starting")
	return nil
}

func (m *Monitoring) printAccessInfo() {
	fmt.Println("\n========================================")
	fmt.Println("Monitoring Stack Access Information")
	fmt.Println("========================================")
	fmt.Println("\n1. Get Istio Ingress IP:")
	fmt.Println("   INGRESS_IP=$(kubectl get svc -n istio-system istio-ingressgateway -o jsonpath='{.status.loadBalancer.ingress[0].ip}')")
	fmt.Println("\n2. Add to /etc/hosts:")
	fmt.Println("   echo \"$INGRESS_IP grafana.local prometheus.local alertmanager.local\" | sudo tee -a /etc/hosts")
	fmt.Println("\n3. Access:")
	fmt.Println("   - Grafana:      http://grafana.local")
	fmt.Println("   - Prometheus:   http://prometheus.local")
	fmt.Println("   - Alertmanager: http://alertmanager.local")
	fmt.Println("\nGrafana Credentials:")
	fmt.Println("  User: admin")
	if m.config.Vault.Enabled() {
		fmt.Println("  Password: (stored in Vault)")
		fmt.Println("  Retrieve: k8s-provisioner vault get-secret k8s-provisioner/api-keys")
		fmt.Println("\nAlertmanager Config:")
		fmt.Println("  Config: (stored in Vault as 'alertmanager_config')")
		fmt.Println("  Store:  vault kv put secret/k8s-provisioner/api-keys alertmanager_config=@alertmanager.yaml")
	} else {
		fmt.Println("  Password: admin123")
		fmt.Println("\nAlertmanager Config:")
		fmt.Println("  Default receiver: null (no notifications)")
		fmt.Println("  To configure: kubectl edit secret alertmanager-alertmanager -n monitoring")
	}
	fmt.Println("========================================")
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
