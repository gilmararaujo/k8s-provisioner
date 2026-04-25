package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Tempo struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewTempo(cfg *config.Config, exec executor.CommandExecutor) *Tempo {
	return &Tempo{config: cfg, exec: exec}
}

func (t *Tempo) Install() error {
	fmt.Println("Installing Tracing Stack (Grafana Tempo + OpenTelemetry Collector)...")

	fmt.Println("Installing Grafana Tempo...")
	if err := t.installTempo(); err != nil {
		return err
	}

	fmt.Println("Installing OpenTelemetry Collector...")
	if err := t.installOtelCollector(); err != nil {
		return err
	}

	fmt.Println("Configuring Tempo datasource in Grafana...")
	if err := t.configureTempoDataSource(); err != nil {
		fmt.Printf("Warning: failed to configure Tempo datasource: %v\n", err)
	}

	fmt.Println("Activating Istio mesh tracing (forwarding to OTel Collector)...")
	if err := t.configureIstioTracing(); err != nil {
		fmt.Printf("Warning: failed to configure Istio tracing: %v\n", err)
	}

	fmt.Println("Waiting for tracing stack to be ready...")
	if err := t.waitForReady(DefaultReadyTimeout); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	fmt.Println("Tracing stack installed successfully!")
	t.printAccessInfo()
	return nil
}

func (t *Tempo) installTempo() error {
	tempo := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: tempo
  namespace: monitoring
automountServiceAccountToken: false
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: tempo-pvc
  namespace: monitoring
spec:
  storageClassName: nfs-dynamic
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 5Gi
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: tempo-config
  namespace: monitoring
data:
  tempo.yaml: |
    server:
      http_listen_port: 3200
    distributor:
      receivers:
        otlp:
          protocols:
            grpc:
              endpoint: 0.0.0.0:4317
            http:
              endpoint: 0.0.0.0:4318
    ingester:
      trace_idle_period: 10s
      max_block_bytes: 1_000_000
      max_block_duration: 5m
    compactor:
      compaction:
        compaction_window: 1h
        max_compaction_objects: 1000000
        block_retention: 24h
        compacted_block_retention: 10m
    storage:
      trace:
        backend: local
        local:
          path: /var/tempo/blocks
        wal:
          path: /var/tempo/wal
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: tempo
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: tempo
  template:
    metadata:
      labels:
        app: tempo
    spec:
      serviceAccountName: tempo
      securityContext:
        runAsNonRoot: true
        fsGroup: 10001
        runAsUser: 10001
        runAsGroup: 10001
      containers:
      - name: tempo
        image: grafana/tempo:2.10.4
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        args:
        - -config.file=/etc/tempo/tempo.yaml
        ports:
        - containerPort: 3200
          name: http
        - containerPort: 4317
          name: otlp-grpc
        - containerPort: 4318
          name: otlp-http
        volumeMounts:
        - name: config
          mountPath: /etc/tempo
        - name: storage
          mountPath: /var/tempo
        - name: tmp
          mountPath: /tmp
        resources:
          requests:
            memory: 256Mi
            cpu: 100m
          limits:
            memory: 512Mi
            cpu: 500m
        readinessProbe:
          httpGet:
            path: /ready
            port: 3200
          initialDelaySeconds: 15
          periodSeconds: 10
      volumes:
      - name: config
        configMap:
          name: tempo-config
      - name: storage
        persistentVolumeClaim:
          claimName: tempo-pvc
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: tempo
  namespace: monitoring
spec:
  type: ClusterIP
  ports:
  - port: 3200
    targetPort: 3200
    name: http
  - port: 4317
    targetPort: 4317
    name: otlp-grpc
  - port: 4318
    targetPort: 4318
    name: otlp-http
  selector:
    app: tempo`

	if err := executor.WriteFile("/tmp/tempo.yaml", tempo); err != nil {
		return err
	}

	_, err := t.exec.RunShell("kubectl apply -f /tmp/tempo.yaml")
	return err
}

func (t *Tempo) installOtelCollector() error {
	otel := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: otel-collector
  namespace: monitoring
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: otel-collector
rules:
- apiGroups: [""]
  resources: [nodes, nodes/proxy, services, endpoints, pods]
  verbs: [get, list, watch]
- apiGroups: [extensions]
  resources: [ingresses]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: otel-collector
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: otel-collector
subjects:
- kind: ServiceAccount
  name: otel-collector
  namespace: monitoring
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: otel-collector-config
  namespace: monitoring
data:
  otel-collector.yaml: |
    receivers:
      otlp:
        protocols:
          grpc:
            endpoint: 0.0.0.0:4317
          http:
            endpoint: 0.0.0.0:4318

    processors:
      batch:
        timeout: 5s
        send_batch_size: 1024
      memory_limiter:
        limit_mib: 256
        check_interval: 5s

    exporters:
      otlp:
        endpoint: tempo.monitoring.svc.cluster.local:4317
        tls:
          insecure: true
      debug:
        verbosity: basic

    service:
      pipelines:
        traces:
          receivers: [otlp]
          processors: [memory_limiter, batch]
          exporters: [otlp]
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: otel-collector
  namespace: monitoring
  labels:
    app: otel-collector
spec:
  selector:
    matchLabels:
      app: otel-collector
  template:
    metadata:
      labels:
        app: otel-collector
    spec:
      serviceAccountName: otel-collector
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        runAsGroup: 65534
        fsGroup: 65534
      containers:
      - name: otel-collector
        image: otel/opentelemetry-collector-contrib:0.149.0
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        args:
        - --config=/etc/otel/otel-collector.yaml
        ports:
        - containerPort: 4317
          hostPort: 4317
          name: otlp-grpc
          protocol: TCP
        - containerPort: 4318
          hostPort: 4318
          name: otlp-http
          protocol: TCP
        volumeMounts:
        - name: config
          mountPath: /etc/otel
        - name: tmp
          mountPath: /tmp
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
          name: otel-collector-config
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: otel-collector
  namespace: monitoring
  labels:
    app: otel-collector
spec:
  type: ClusterIP
  ports:
  - port: 4317
    targetPort: 4317
    name: grpc-otlp
    appProtocol: grpc
  - port: 4318
    targetPort: 4318
    name: http-otlp
  selector:
    app: otel-collector
---
apiVersion: networking.istio.io/v1beta1
kind: DestinationRule
metadata:
  name: otel-collector-plaintext
  namespace: monitoring
spec:
  host: otel-collector.monitoring.svc.cluster.local
  trafficPolicy:
    tls:
      mode: DISABLE`

	if err := executor.WriteFile("/tmp/otel-collector.yaml", otel); err != nil {
		return err
	}

	_, err := t.exec.RunShell("kubectl apply -f /tmp/otel-collector.yaml")
	return err
}

// configureTempoDataSource atualiza o ConfigMap do Grafana com Prometheus + Loki + Tempo.
// UIDs fixos permitem correlação entre traces, logs e métricas.
func (t *Tempo) configureTempoDataSource() error {
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
      isDefault: false
      jsonData:
        derivedFields:
        - datasourceUid: tempo-uid
          matcherRegex: "traceID=(\\w+)"
          name: TraceID
          url: "${__value.raw}"
    - name: Tempo
      type: tempo
      uid: tempo-uid
      access: proxy
      url: http://tempo:3200
      isDefault: false
      jsonData:
        tracesToLogsV2:
          datasourceUid: loki-uid
          tags:
          - key: service.name
            value: app
          - key: k8s.namespace.name
            value: namespace
          filterByTraceID: true
          filterBySpanID: false
        tracesToMetrics:
          datasourceUid: prometheus-uid
          tags:
          - key: service.name
            value: service
        serviceMap:
          datasourceUid: prometheus-uid
        nodeGraph:
          enabled: true`

	if err := executor.WriteFile("/tmp/grafana-datasources-full.yaml", datasources); err != nil {
		return err
	}

	if _, err := t.exec.RunShell("kubectl apply -f /tmp/grafana-datasources-full.yaml"); err != nil {
		return err
	}

	// Reinicia o Grafana para carregar os novos datasources
	_, err := t.exec.RunShell("kubectl rollout restart deployment/grafana -n monitoring")
	return err
}

// configureIstioTracing activates the otel-tracing extension provider (defined at Istio install
// time) for the entire mesh. All namespaces with istio-injection=enabled will have their
// sidecar proxies automatically forward spans to the OTel Collector → Tempo.
func (t *Tempo) configureIstioTracing() error {
	telemetry := `apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata:
  name: mesh-default
  namespace: istio-system
spec:
  tracing:
  - providers:
    - name: otel-tracing
    randomSamplingPercentage: 100.0`

	if err := executor.WriteFile("/tmp/istio-telemetry.yaml", telemetry); err != nil {
		return err
	}

	_, err := t.exec.RunShell("kubectl apply -f /tmp/istio-telemetry.yaml")
	return err
}

func (t *Tempo) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := t.exec.RunShell("kubectl get pods -n monitoring -l app=tempo -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out != "Running" {
			fmt.Println("Waiting for Tempo...")
			time.Sleep(DefaultPollInterval)
			continue
		}
		fmt.Println("Tracing stack is ready!")
		return nil
	}
	return fmt.Errorf("timeout waiting for Tempo")
}

func (t *Tempo) printAccessInfo() {
	fmt.Println("\n========================================")
	fmt.Println("Tracing Stack Access Information")
	fmt.Println("========================================")
	fmt.Println("\nAcesse traces via Grafana:")
	fmt.Println("  1. Abra o Grafana (http://grafana.local)")
	fmt.Println("  2. Vá em Explore (sidebar esquerda)")
	fmt.Println("  3. Selecione 'Tempo' como datasource")
	fmt.Println("  4. Busque por TraceID ou use Service Graph")
	fmt.Println("\nEnviar traces das suas apps:")
	fmt.Println("  OTLP gRPC: otel-collector.monitoring.svc:4317")
	fmt.Println("  OTLP HTTP: otel-collector.monitoring.svc:4318")
	fmt.Println("  OTLP gRPC (host): <node-ip>:4317 (via hostPort)")
	fmt.Println("\nCorrelações habilitadas:")
	fmt.Println("  Traces → Logs  (Tempo → Loki via TraceID)")
	fmt.Println("  Traces → Métricas (Tempo → Prometheus via service.name)")
	fmt.Println("  Service Map (via Prometheus metrics)")
	fmt.Println("========================================")
}
