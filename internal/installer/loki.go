package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Loki struct {
	config *config.Config
	exec   *executor.Executor
}

func NewLoki(cfg *config.Config, exec *executor.Executor) *Loki {
	return &Loki{config: cfg, exec: exec}
}

func (l *Loki) Install() error {
	fmt.Println("Installing Loki Stack (Loki + Promtail)...")

	// Install Loki
	fmt.Println("Installing Loki...")
	if err := l.installLoki(); err != nil {
		return err
	}

	// Install Promtail
	fmt.Println("Installing Promtail...")
	if err := l.installPromtail(); err != nil {
		return err
	}

	// Add Loki datasource to Grafana
	fmt.Println("Configuring Loki datasource in Grafana...")
	if err := l.configureLokiDatasource(); err != nil {
		fmt.Printf("Warning: Failed to configure Loki datasource: %v\n", err)
	}

	// Wait for components to be ready
	fmt.Println("Waiting for Loki stack to be ready...")
	if err := l.waitForReady(3 * time.Minute); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	fmt.Println("Loki stack installed successfully!")
	l.printAccessInfo()
	return nil
}

func (l *Loki) installLoki() error {
	loki := `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: loki-pvc
  namespace: monitoring
spec:
  storageClassName: nfs-storage
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: loki-config
  namespace: monitoring
data:
  loki.yaml: |
    auth_enabled: false
    server:
      http_listen_port: 3100
    ingester:
      lifecycler:
        address: 127.0.0.1
        ring:
          kvstore:
            store: inmemory
          replication_factor: 1
        final_sleep: 0s
      chunk_idle_period: 5m
      chunk_retain_period: 30s
      wal:
        dir: /data/loki/wal
    schema_config:
      configs:
        - from: 2020-05-15
          store: boltdb
          object_store: filesystem
          schema: v11
          index:
            prefix: index_
            period: 168h
    storage_config:
      boltdb:
        directory: /data/loki/index
      filesystem:
        directory: /data/loki/chunks
    limits_config:
      enforce_metric_name: false
      reject_old_samples: true
      reject_old_samples_max_age: 168h
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: loki
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: loki
  template:
    metadata:
      labels:
        app: loki
    spec:
      securityContext:
        fsGroup: 10001
        runAsGroup: 10001
        runAsUser: 10001
      containers:
      - name: loki
        image: grafana/loki:2.9.0
        args:
        - -config.file=/etc/loki/loki.yaml
        ports:
        - containerPort: 3100
          name: http
        volumeMounts:
        - name: config
          mountPath: /etc/loki
        - name: storage
          mountPath: /data/loki
        resources:
          requests:
            memory: 256Mi
            cpu: 100m
          limits:
            memory: 512Mi
            cpu: 500m
      volumes:
      - name: config
        configMap:
          name: loki-config
      - name: storage
        persistentVolumeClaim:
          claimName: loki-pvc
---
apiVersion: v1
kind: Service
metadata:
  name: loki
  namespace: monitoring
spec:
  type: ClusterIP
  ports:
  - port: 3100
    targetPort: 3100
    name: http
  selector:
    app: loki`

	if err := executor.WriteFile("/tmp/loki.yaml", loki); err != nil {
		return err
	}

	_, err := l.exec.RunShell("kubectl apply -f /tmp/loki.yaml")
	return err
}

func (l *Loki) installPromtail() error {
	promtail := `apiVersion: v1
kind: ConfigMap
metadata:
  name: promtail-config
  namespace: monitoring
data:
  promtail.yaml: |
    server:
      http_listen_port: 9080
      grpc_listen_port: 0
    positions:
      filename: /tmp/positions.yaml
    clients:
      - url: http://loki:3100/loki/api/v1/push
    scrape_configs:
      - job_name: kubernetes-pods
        kubernetes_sd_configs:
          - role: pod
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_node_name]
            target_label: __host__
          - action: labelmap
            regex: __meta_kubernetes_pod_label_(.+)
          - action: replace
            source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - action: replace
            source_labels: [__meta_kubernetes_pod_name]
            target_label: pod
          - action: replace
            source_labels: [__meta_kubernetes_pod_container_name]
            target_label: container
        pipeline_stages:
          - docker: {}
      - job_name: kubernetes-system
        static_configs:
          - targets:
              - localhost
            labels:
              job: varlogs
              __path__: /var/log/*.log
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: promtail
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: promtail
rules:
- apiGroups: [""]
  resources:
  - nodes
  - nodes/proxy
  - services
  - endpoints
  - pods
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: promtail
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: promtail
subjects:
- kind: ServiceAccount
  name: promtail
  namespace: monitoring
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: promtail
  namespace: monitoring
spec:
  selector:
    matchLabels:
      app: promtail
  template:
    metadata:
      labels:
        app: promtail
    spec:
      serviceAccountName: promtail
      containers:
      - name: promtail
        image: grafana/promtail:2.9.0
        args:
        - -config.file=/etc/promtail/promtail.yaml
        ports:
        - containerPort: 9080
          name: http
        volumeMounts:
        - name: config
          mountPath: /etc/promtail
        - name: varlog
          mountPath: /var/log
          readOnly: true
        - name: varlibdockercontainers
          mountPath: /var/lib/docker/containers
          readOnly: true
        - name: pods
          mountPath: /var/log/pods
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
      - name: config
        configMap:
          name: promtail-config
      - name: varlog
        hostPath:
          path: /var/log
      - name: varlibdockercontainers
        hostPath:
          path: /var/lib/docker/containers
      - name: pods
        hostPath:
          path: /var/log/pods`

	if err := executor.WriteFile("/tmp/promtail.yaml", promtail); err != nil {
		return err
	}

	_, err := l.exec.RunShell("kubectl apply -f /tmp/promtail.yaml")
	return err
}

func (l *Loki) configureLokiDatasource() error {
	// Restart Grafana with updated datasources
	datasources := `apiVersion: v1
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
    - name: Loki
      type: loki
      access: proxy
      url: http://loki:3100
      isDefault: false`

	if err := executor.WriteFile("/tmp/grafana-datasources.yaml", datasources); err != nil {
		return err
	}

	if _, err := l.exec.RunShell("kubectl apply -f /tmp/grafana-datasources.yaml"); err != nil {
		return err
	}

	// Restart Grafana to pick up new datasource
	_, err := l.exec.RunShell("kubectl rollout restart deployment/grafana -n monitoring")
	return err
}

func (l *Loki) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Check Loki
		out, _ := l.exec.RunShell("kubectl get pods -n monitoring -l app=loki -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Loki...")
			time.Sleep(10 * time.Second)
			continue
		}

		// Check Promtail (at least one running)
		out, _ = l.exec.RunShell("kubectl get pods -n monitoring -l app=promtail -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Promtail...")
			time.Sleep(10 * time.Second)
			continue
		}

		fmt.Println("Loki stack is ready!")
		return nil
	}

	return fmt.Errorf("timeout waiting for Loki stack")
}

func (l *Loki) printAccessInfo() {
	fmt.Println("\n========================================")
	fmt.Println("Loki Stack Access Information")
	fmt.Println("========================================")
	fmt.Println("\nAccess logs via Grafana:")
	fmt.Println("  1. Open Grafana (http://grafana.local)")
	fmt.Println("  2. Go to Explore (left sidebar)")
	fmt.Println("  3. Select 'Loki' as datasource")
	fmt.Println("\nExample LogQL queries:")
	fmt.Println("  {namespace=\"default\"}")
	fmt.Println("  {namespace=\"kube-system\"}")
	fmt.Println("  {pod=~\"nginx.*\"}")
	fmt.Println("  {container=\"app\"} |= \"error\"")
	fmt.Println("========================================")
}
