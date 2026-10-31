package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

const (
	externalDNSNamespace = "external-dns"
	// externalDNSEtcdSecret holds the etcd CA plus the client cert/key that
	// vagrant/scripts/storage-dns.sh issues for ExternalDNS.
	externalDNSEtcdSecret = "external-dns-etcd"
	etcdTLSDirRemote      = "/etc/etcd/tls"
)

// ExternalDNS publishes every Istio Gateway host (and annotated LoadBalancer
// Services) to the lab DNS on the storage node: it writes SkyDNS records into
// etcd, which the CoreDNS there serves for the lab domain.
type ExternalDNS struct {
	config *config.Config
	exec   executor.ShellExecutor
}

func NewExternalDNS(cfg *config.Config, exec executor.ShellExecutor) *ExternalDNS {
	return &ExternalDNS{config: cfg, exec: exec}
}

func (e *ExternalDNS) Install() error {
	fmt.Println("Installing ExternalDNS (CoreDNS/etcd provider on the storage node)...")

	if err := e.createEtcdSecret(); err != nil {
		return fmt.Errorf("etcd client credentials: %w", err)
	}
	if _, err := e.exec.RunShellWithStdin("kubectl apply -f -", e.manifest()); err != nil {
		return fmt.Errorf("apply ExternalDNS manifests: %w", err)
	}

	fmt.Println("Waiting for ExternalDNS to be ready...")
	if err := e.waitForReady(shortReadyTimeout); err != nil {
		return err
	}

	e.printAccessInfo()
	return nil
}

// createEtcdSecret copies the etcd CA and the ExternalDNS client cert/key from
// the storage node into a Secret. Piped via stdin so the key never lands in /tmp.
func (e *ExternalDNS) createEtcdSecret() error {
	files := map[string]string{"ca.crt": "ca.crt", "tls.crt": "external-dns.crt", "tls.key": "external-dns.key"}
	data := make(map[string]string, len(files))
	for key, name := range files {
		out, err := runOnStorage(e.config, e.exec, "sudo cat "+etcdTLSDirRemote+"/"+name)
		if err != nil {
			return fmt.Errorf("fetch %s from the storage node: %w", name, err)
		}
		if !strings.Contains(out, "-----BEGIN ") {
			return fmt.Errorf("%s from the storage node is not PEM (was vagrant/scripts/storage-dns.sh run?)", name)
		}
		data[key] = strings.TrimSpace(out) + "\n"
	}

	secret := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
---
apiVersion: v1
kind: Secret
metadata:
  name: %[2]s
  namespace: %[1]s
type: Opaque
stringData:
  ca.crt: %[3]q
  tls.crt: %[4]q
  tls.key: %[5]q`, externalDNSNamespace, externalDNSEtcdSecret, data["ca.crt"], data["tls.crt"], data["tls.key"])
	_, err := e.exec.RunShellWithStdin("kubectl apply -f -", secret)
	return err
}

func (e *ExternalDNS) manifest() string {
	version := e.config.Versions.ExternalDNS
	if version == "" {
		version = "v0.23.0"
	}
	return fmt.Sprintf(`apiVersion: v1
kind: ServiceAccount
metadata:
  name: external-dns
  namespace: %[1]s
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: external-dns
rules:
- apiGroups: [""]
  resources: [services, endpoints, pods, nodes]
  verbs: [get, watch, list]
- apiGroups: [discovery.k8s.io]
  resources: [endpointslices]
  verbs: [get, watch, list]
- apiGroups: [networking.k8s.io]   # the istio-gateway source also watches Ingresses
  resources: [ingresses]
  verbs: [get, watch, list]
- apiGroups: [networking.istio.io]
  resources: [gateways, virtualservices]
  verbs: [get, watch, list]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: external-dns
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: external-dns
subjects:
- kind: ServiceAccount
  name: external-dns
  namespace: %[1]s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: external-dns
  namespace: %[1]s
  labels:
    app: external-dns
spec:
  replicas: 1
  # A single writer owns the records; never run two side by side.
  strategy:
    type: Recreate
  selector:
    matchLabels:
      app: external-dns
  template:
    metadata:
      labels:
        app: external-dns
    spec:
      serviceAccountName: external-dns
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        runAsGroup: 65534
        fsGroup: 65534
      containers:
      - name: external-dns
        image: registry.k8s.io/external-dns/external-dns:%[2]s
        args:
        - --source=istio-gateway
        - --source=service
        - --provider=coredns
        - --domain-filter=%[3]s
        - --policy=sync
        - --registry=txt
        - --txt-owner-id=%[4]s
        - --interval=30s
        - --metrics-address=:7979
        ports:
        - name: http-metrics
          containerPort: 7979
        env:
        - name: ETCD_URLS
          value: https://%[5]s:2379
        - name: ETCD_CA_FILE
          value: /etc/etcd-tls/ca.crt
        - name: ETCD_CERT_FILE
          value: /etc/etcd-tls/tls.crt
        - name: ETCD_KEY_FILE
          value: /etc/etcd-tls/tls.key
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        volumeMounts:
        - name: etcd-tls
          mountPath: /etc/etcd-tls
          readOnly: true
        resources:
          requests:
            memory: 32Mi
            cpu: 10m
          limits:
            memory: 128Mi
            cpu: 100m
      volumes:
      - name: etcd-tls
        secret:
          secretName: %[6]s
          defaultMode: 0400
---
# Scraped by the external-dns ServiceMonitor (installed with the monitoring stack).
apiVersion: v1
kind: Service
metadata:
  name: external-dns
  namespace: %[1]s
  labels:
    app: external-dns
spec:
  selector:
    app: external-dns
  ports:
  - name: http-metrics
    port: 7979
    targetPort: http-metrics`,
		externalDNSNamespace, version, e.config.Domain(), e.ownerID(), e.config.StorageIP(), externalDNSEtcdSecret)
}

// ownerID tags the records this cluster owns, so a rebuilt cluster with the same
// name takes over its predecessor's records instead of leaving them orphaned.
func (e *ExternalDNS) ownerID() string {
	if e.config.Cluster.Name != "" {
		return e.config.Cluster.Name
	}
	return "k8s-lab"
}

func (e *ExternalDNS) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := e.exec.RunShell(fmt.Sprintf(
			"kubectl get deployment external-dns -n %s -o jsonpath='{.status.readyReplicas}' 2>/dev/null", externalDNSNamespace))
		if err == nil && strings.TrimSpace(out) == "1" {
			return nil
		}
		fmt.Println("Waiting for ExternalDNS...")
		time.Sleep(shortPollInterval)
	}
	return fmt.Errorf("timeout waiting for ExternalDNS")
}

func (e *ExternalDNS) printAccessInfo() {
	domain := e.config.Domain()
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   ExternalDNS installed!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Printf("\nEvery Istio Gateway host under %s is published to the lab DNS at %s:53.\n", domain, e.config.StorageIP())
	fmt.Printf("Point your host at it once (macOS):\n")
	fmt.Printf("  sudo mkdir -p /etc/resolver && echo 'nameserver %s' | sudo tee /etc/resolver/%s\n", e.config.StorageIP(), domain)
	fmt.Printf("\nCheck: dig @%s %s +short\n", e.config.StorageIP(), e.config.Host("grafana"))
}
