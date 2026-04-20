package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Loki struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewLoki(cfg *config.Config, exec executor.CommandExecutor) *Loki {
	return &Loki{config: cfg, exec: exec}
}

func (l *Loki) Install() error {
	fmt.Println("Installing Loki Stack (Loki + Grafana Alloy)...")

	fmt.Println("Installing Loki...")
	if err := l.installLoki(); err != nil {
		return err
	}

	fmt.Println("Installing Grafana Alloy (log collector)...")
	if err := l.installAlloy(); err != nil {
		return err
	}

	fmt.Println("Configuring Loki datasource in Grafana...")
	if err := l.configureLokiDatasource(); err != nil {
		fmt.Printf("Warning: Failed to configure Loki datasource: %v\n", err)
	}

	fmt.Println("Waiting for Loki stack to be ready...")
	if err := l.waitForReady(ShortReadyTimeout); err != nil {
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
  storageClassName: nfs-dynamic
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
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
      grpc_listen_port: 9095

    common:
      instance_addr: 127.0.0.1
      path_prefix: /data/loki
      storage:
        filesystem:
          chunks_directory: /data/loki/chunks
          rules_directory: /data/loki/rules
      replication_factor: 1
      ring:
        kvstore:
          store: inmemory

    query_range:
      results_cache:
        cache:
          embedded_cache:
            enabled: true
            max_size_mb: 100

    schema_config:
      configs:
        - from: 2024-01-01
          store: tsdb
          object_store: filesystem
          schema: v13
          index:
            prefix: index_
            period: 24h

    limits_config:
      reject_old_samples: true
      reject_old_samples_max_age: 168h
      allow_structured_metadata: true
      volume_enabled: true

    analytics:
      reporting_enabled: false
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: loki
  namespace: monitoring
automountServiceAccountToken: false
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
      serviceAccountName: loki
      securityContext:
        runAsNonRoot: true
        fsGroup: 10001
        runAsGroup: 10001
        runAsUser: 10001
      containers:
      - name: loki
        image: grafana/loki:3.7.1
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        args:
        - -config.file=/etc/loki/loki.yaml
        ports:
        - containerPort: 3100
          name: http
        - containerPort: 9095
          name: grpc
        volumeMounts:
        - name: config
          mountPath: /etc/loki
        - name: storage
          mountPath: /data/loki
        - name: tmp
          mountPath: /tmp
        readinessProbe:
          httpGet:
            path: /ready
            port: 3100
          initialDelaySeconds: 15
          periodSeconds: 10
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
      - name: tmp
        emptyDir: {}
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
  - port: 9095
    targetPort: 9095
    name: grpc
  selector:
    app: loki`

	if err := executor.WriteFile("/tmp/loki.yaml", loki); err != nil {
		return err
	}

	_, err := l.exec.RunShell("kubectl apply -f /tmp/loki.yaml")
	return err
}

func (l *Loki) installAlloy() error {
	alloy := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: alloy
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: alloy
rules:
- apiGroups: [""]
  resources:
  - nodes
  - nodes/proxy
  - nodes/log
  - services
  - endpoints
  - pods
  - pods/log
  - events
  verbs: [get, list, watch]
- apiGroups: [apps]
  resources: [deployments, replicasets, statefulsets, daemonsets]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: alloy
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: alloy
subjects:
- kind: ServiceAccount
  name: alloy
  namespace: monitoring
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: alloy-config
  namespace: monitoring
data:
  config.alloy: |
    // Discover all Kubernetes pods
    discovery.kubernetes "pods" {
      role = "pod"
    }

    // Relabel pod metadata into Loki labels
    discovery.relabel "pods" {
      targets = discovery.kubernetes.pods.targets

      rule {
        source_labels = ["__meta_kubernetes_namespace"]
        target_label  = "namespace"
      }
      rule {
        source_labels = ["__meta_kubernetes_pod_name"]
        target_label  = "pod"
      }
      rule {
        source_labels = ["__meta_kubernetes_pod_container_name"]
        target_label  = "container"
      }
      rule {
        source_labels = ["__meta_kubernetes_pod_node_name"]
        target_label  = "node"
      }
      // Drop pods that are not running
      rule {
        source_labels = ["__meta_kubernetes_pod_phase"]
        regex         = "Pending|Succeeded|Failed|Completed"
        action        = "drop"
      }
    }

    // Collect logs from discovered pods
    loki.source.kubernetes "pods" {
      targets    = discovery.relabel.pods.output
      forward_to = [loki.write.default.receiver]
    }

    // Collect Kubernetes events as logs
    loki.source.kubernetes_events "events" {
      job_name   = "integrations/kubernetes/eventhandler"
      log_format = "logfmt"
      forward_to = [loki.write.default.receiver]
    }

    // Write to Loki
    loki.write "default" {
      endpoint {
        url = "http://loki.monitoring.svc:3100/loki/api/v1/push"
      }
    }
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: alloy
  namespace: monitoring
  labels:
    app: alloy
spec:
  selector:
    matchLabels:
      app: alloy
  template:
    metadata:
      labels:
        app: alloy
    spec:
      serviceAccountName: alloy
      securityContext:
        runAsNonRoot: true
        runAsUser: 473
        runAsGroup: 473
        fsGroup: 473
      containers:
      - name: alloy
        image: grafana/alloy:v1.15.1
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        args:
        - run
        - /etc/alloy/config.alloy
        - --storage.path=/var/lib/alloy/data
        - --server.http.listen-addr=0.0.0.0:12345
        ports:
        - containerPort: 12345
          name: http
        volumeMounts:
        - name: config
          mountPath: /etc/alloy
        - name: alloy-data
          mountPath: /var/lib/alloy/data
        - name: tmp
          mountPath: /tmp
        env:
        - name: HOSTNAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        resources:
          requests:
            memory: 64Mi
            cpu: 50m
          limits:
            memory: 256Mi
            cpu: 200m
      tolerations:
      - effect: NoSchedule
        operator: Exists
      volumes:
      - name: config
        configMap:
          name: alloy-config
      - name: alloy-data
        emptyDir: {}
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: alloy
  namespace: monitoring
  labels:
    app: alloy
spec:
  type: ClusterIP
  ports:
  - port: 12345
    targetPort: 12345
    name: http
  selector:
    app: alloy`

	if err := executor.WriteFile("/tmp/alloy.yaml", alloy); err != nil {
		return err
	}

	_, err := l.exec.RunShell("kubectl apply -f /tmp/alloy.yaml")
	return err
}

func (l *Loki) configureLokiDatasource() error {
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
      uid: prometheus-uid
      access: proxy
      url: http://prometheus:9090
      isDefault: true
    - name: Loki
      type: loki
      uid: loki-uid
      access: proxy
      url: http://loki:3100
      isDefault: false`

	if err := executor.WriteFile("/tmp/grafana-datasources.yaml", datasources); err != nil {
		return err
	}

	if _, err := l.exec.RunShell("kubectl apply -f /tmp/grafana-datasources.yaml"); err != nil {
		return err
	}

	_, err := l.exec.RunShell("kubectl rollout restart deployment/grafana -n monitoring")
	return err
}

func (l *Loki) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := l.exec.RunShell("kubectl get pods -n monitoring -l app=loki -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Loki...")
			time.Sleep(DefaultPollInterval)
			continue
		}

		out, _ = l.exec.RunShell("kubectl get pods -n monitoring -l app=alloy -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Alloy...")
			time.Sleep(DefaultPollInterval)
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
	fmt.Println("\nAlloy UI (log pipeline status):")
	fmt.Println("  kubectl port-forward -n monitoring svc/alloy 12345:12345")
	fmt.Println("  Open: http://localhost:12345")
	fmt.Println("\nExample LogQL queries:")
	fmt.Println("  {namespace=\"default\"}")
	fmt.Println("  {namespace=\"kube-system\"}")
	fmt.Println("  {pod=~\"nginx.*\"}")
	fmt.Println("  {container=\"app\"} |= \"error\"")
	fmt.Println("========================================")
}
