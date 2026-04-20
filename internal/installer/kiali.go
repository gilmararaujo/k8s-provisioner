package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Kiali struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewKiali(cfg *config.Config, exec executor.CommandExecutor) *Kiali {
	return &Kiali{config: cfg, exec: exec}
}

func (k *Kiali) Install() error {
	fmt.Println("Installing Kiali (Service Mesh Observability)...")

	if err := k.installKiali(); err != nil {
		return err
	}

	if err := k.configureIngress(); err != nil {
		fmt.Printf("Warning: failed to configure Kiali ingress: %v\n", err)
	}

	fmt.Println("Waiting for Kiali to be ready...")
	if err := k.waitForReady(DefaultReadyTimeout); err != nil {
		fmt.Printf("Warning: %v\n", err)
	}

	fmt.Println("Kiali installed successfully!")
	k.printAccessInfo()
	return nil
}

func (k *Kiali) installKiali() error {
	tracingEnabled := k.config.Components.Tracing == "otel-tempo"
	loggingEnabled := k.config.Components.Logging == "loki"

	// Kiali v2.x config changes vs v1.x:
	// - deployment.accessible_namespaces removed → cluster_wide_access: true
	// - external_services.logging_backend renamed to external_services.logging
	// - tracing.tempo_config restructured
	tracingSection := ""
	if tracingEnabled {
		tracingSection = `
      tracing:
        enabled: true
        provider: "tempo"
        internal_url: "http://tempo.monitoring:3200"
        use_grpc: false
        tempo_config:
          datasource_uid: "tempo-uid"
          org_id: "1"
        query_scope:
          mesh_id: ""
          cluster: ""`
	}

	loggingSection := ""
	if loggingEnabled {
		loggingSection = `
      logging:
        enabled: true
        use_grpc: false
        url: "http://loki.monitoring:3100"`
	}

	kiali := fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: kiali
  namespace: istio-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kiali
rules:
- apiGroups: [""]
  resources:
  - configmaps
  - endpoints
  - namespaces
  - nodes
  - pods
  - pods/log
  - replicationcontrollers
  - services
  - serviceaccounts
  verbs: [get, list, watch]
- apiGroups: [apps]
  resources:
  - daemonsets
  - deployments
  - replicasets
  - statefulsets
  verbs: [get, list, watch]
- apiGroups: [autoscaling]
  resources: [horizontalpodautoscalers]
  verbs: [get, list, watch]
- apiGroups: [batch]
  resources: [cronjobs, jobs]
  verbs: [get, list, watch]
- apiGroups: [networking.k8s.io]
  resources: [ingresses, ingressclasses]
  verbs: [get, list, watch]
- apiGroups: [networking.istio.io, security.istio.io, extensions.istio.io, telemetry.istio.io]
  resources: ["*"]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [gateway.networking.k8s.io]
  resources: [gateways, httproutes, grpcroutes, referencegrants, tcproutes, tlsroutes]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [rbac.authorization.k8s.io]
  resources: [clusterrolebindings, clusterroles, rolebindings, roles]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kiali
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: kiali
subjects:
- kind: ServiceAccount
  name: kiali
  namespace: istio-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kiali-controlplane
  namespace: istio-system
rules:
- apiGroups: [""]
  resources: [configmaps, endpoints, pods, pods/portforward, services, secrets]
  verbs: [get, list, watch, create, update, patch, delete]
- apiGroups: [apps]
  resources: [deployments, replicasets]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kiali-controlplane
  namespace: istio-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: kiali-controlplane
subjects:
- kind: ServiceAccount
  name: kiali
  namespace: istio-system
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: kiali
  namespace: istio-system
data:
  config.yaml: |
    auth:
      strategy: anonymous
    deployment:
      cluster_wide_access: true
      namespace: istio-system
    external_services:
      custom_dashboards:
        enabled: true
      grafana:
        enabled: true
        internal_url: "http://grafana.monitoring:3000"
        external_url: "http://grafana.local"%s%s
      istio:
        root_namespace: istio-system
        istio_status_enabled: true
        url_service_version: "http://istiod.istio-system:15014/version"
      prometheus:
        url: "http://prometheus.monitoring:9090"
    istio_namespace: istio-system
    kiali_feature_flags:
      certificates_information_indicators:
        enabled: true
      clustering:
        autodetect_secrets:
          enabled: false
    server:
      port: 20001
      web_root: "/kiali"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kiali
  namespace: istio-system
  labels:
    app: kiali
    version: v2.24.0
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kiali
  template:
    metadata:
      labels:
        app: kiali
        version: v2.24.0
    spec:
      serviceAccountName: kiali
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        runAsGroup: 1000
        fsGroup: 1000
      containers:
      - name: kiali
        image: quay.io/kiali/kiali:v2.24.0
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        command:
        - /opt/kiali/kiali
        - -config
        - /kiali-configuration/config.yaml
        ports:
        - name: api-port
          containerPort: 20001
          protocol: TCP
        - name: http-metrics
          containerPort: 9090
          protocol: TCP
        readinessProbe:
          httpGet:
            path: /kiali/healthz
            port: api-port
          initialDelaySeconds: 15
          periodSeconds: 10
        livenessProbe:
          httpGet:
            path: /kiali/healthz
            port: api-port
          initialDelaySeconds: 30
          periodSeconds: 30
        volumeMounts:
        - name: kiali-configuration
          mountPath: /kiali-configuration
        - name: tmp
          mountPath: /tmp
        resources:
          requests:
            memory: 128Mi
            cpu: 100m
          limits:
            memory: 512Mi
            cpu: 500m
      volumes:
      - name: kiali-configuration
        configMap:
          name: kiali
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: kiali
  namespace: istio-system
  labels:
    app: kiali
spec:
  type: ClusterIP
  ports:
  - name: http
    port: 20001
    targetPort: 20001
  selector:
    app: kiali`, tracingSection, loggingSection)

	if err := executor.WriteFile("/tmp/kiali.yaml", kiali); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/kiali.yaml")
	return err
}

func (k *Kiali) configureIngress() error {
	ingress := `apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: kiali-gateway
  namespace: istio-system
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - "kiali.local"
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: kiali
  namespace: istio-system
spec:
  hosts:
  - "kiali.local"
  gateways:
  - kiali-gateway
  http:
  - match:
    - uri:
        prefix: /
    route:
    - destination:
        host: kiali
        port:
          number: 20001`

	if err := executor.WriteFile("/tmp/kiali-ingress.yaml", ingress); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/kiali-ingress.yaml")
	return err
}

func (k *Kiali) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := k.exec.RunShell("kubectl get pods -n istio-system -l app=kiali -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out == "Running" {
			fmt.Println("Kiali is ready!")
			return nil
		}
		fmt.Println("Waiting for Kiali...")
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for Kiali")
}

func (k *Kiali) printAccessInfo() {
	fmt.Println("\n========================================")
	fmt.Println("Kiali Access Information")
	fmt.Println("========================================")
	fmt.Println("\nService Mesh Observability:")
	fmt.Println("  1. Add to /etc/hosts:")
	fmt.Println("     <ingress-ip> kiali.local")
	fmt.Println("  2. Open: http://kiali.local/kiali")
	fmt.Println("\nIntegrations active:")
	fmt.Println("  Metrics  → Prometheus (http://prometheus.monitoring:9090)")
	fmt.Println("  Dashboards → Grafana (http://grafana.local)")
	if k.config.Components.Tracing == "otel-tempo" {
		fmt.Println("  Traces   → Grafana Tempo (http://tempo.monitoring:3200)")
	}
	if k.config.Components.Logging == "loki" {
		fmt.Println("  Logs     → Loki (http://loki.monitoring:3100)")
	}
	fmt.Println("\nFeatures:")
	fmt.Println("  Service Graph  - visual topology of the mesh")
	fmt.Println("  Traffic Metrics - RPS, error rate, latency per service")
	fmt.Println("  Config Validation - detects misconfigured Istio resources")
	fmt.Println("  Workload Details  - drill down into any pod/deployment")
	fmt.Println("========================================")
}
