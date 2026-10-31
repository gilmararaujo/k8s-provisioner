package installer

import (
	"fmt"
	"strings"
)

// blackboxJob is the Prometheus job of every TLS probe; the dashboard's
// Certificates row selects on it.
const blackboxJob = "blackbox"

// tlsProbeGroup is one set of TLS endpoints probed for certificate expiry.
type tlsProbeGroup struct {
	name    string // Probe object name and the "component" label
	targets []string
}

// installCertificateProbes deploys blackbox_exporter and probes every TLS
// endpoint whose certificate cert-manager does not report: the apiserver and
// kubelets (kubeadm), Vault (storage node) and each published lab host, which
// shows the certificate clients actually receive.
//
// The probes read expiry dates only, so they skip chain verification: kubelet
// serving certificates are self-signed per node and there is no CA to check
// them against.
func (m *Monitoring) installCertificateProbes() error {
	version := m.config.Versions.BlackboxExporter
	if version == "" {
		version = "v0.29.1"
	}
	docs := []string{fmt.Sprintf(blackboxManifest, version)}
	for _, g := range m.tlsProbeGroups() {
		docs = append(docs, tlsProbe(g))
	}
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", strings.Join(docs, "\n---\n")); err != nil {
		return fmt.Errorf("apply certificate probes: %w", err)
	}
	return nil
}

// tlsProbeGroups lists the endpoints to probe, from the nodes and the
// components this config installs.
func (m *Monitoring) tlsProbeGroups() []tlsProbeGroup {
	cfg := m.config
	var groups []tlsProbeGroup
	if cp := cfg.ControlPlaneNode(); cp != nil {
		groups = append(groups, tlsProbeGroup{"apiserver", []string{cp.IP + ":6443"}})
	}

	var kubelets []string
	for _, n := range cfg.Nodes {
		if n.Role == "controlplane" || n.Role == "worker" {
			kubelets = append(kubelets, n.IP+":10250")
		}
	}
	if len(kubelets) > 0 {
		groups = append(groups, tlsProbeGroup{"kubelet", kubelets})
	}

	if cfg.Vault.Enabled && cfg.StorageIP() != "" {
		groups = append(groups, tlsProbeGroup{"vault", []string{cfg.StorageIP() + ":8200"}})
	}

	if cfg.Components.ServiceMesh == "istio" {
		hosts := []string{"grafana", "prometheus", "alertmanager", "kiali"}
		if cfg.Components.Keycloak == "enabled" {
			hosts = append(hosts, "keycloak")
		}
		if cfg.Components.Karpor == "enabled" {
			hosts = append(hosts, "karpor")
		}
		targets := make([]string, len(hosts))
		for i, h := range hosts {
			// host:port, so the probe sends the hostname as SNI and the Gateway
			// returns that host's certificate.
			targets[i] = cfg.Host(h) + ":443"
		}
		groups = append(groups, tlsProbeGroup{"gateway", targets})
	}
	return groups
}

func tlsProbe(g tlsProbeGroup) string {
	return fmt.Sprintf(`apiVersion: monitoring.coreos.com/v1
kind: Probe
metadata:
  name: tls-%[1]s
  namespace: monitoring
spec:
  jobName: %[2]s
  interval: 5m
  module: tls
  prober:
    url: blackbox-exporter.monitoring.svc:9115
  targets:
    staticConfig:
      static: ["%[3]s"]
      labels:
        component: %[1]s`, g.name, blackboxJob, strings.Join(g.targets, `", "`))
}

const blackboxManifest = `apiVersion: v1
kind: ConfigMap
metadata:
  name: blackbox-exporter
  namespace: monitoring
data:
  blackbox.yml: |
    modules:
      tls:
        prober: tcp
        timeout: 10s
        tcp:
          tls: true
          tls_config:
            insecure_skip_verify: true
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: blackbox-exporter
  namespace: monitoring
  labels:
    app: blackbox-exporter
spec:
  replicas: 1
  selector:
    matchLabels:
      app: blackbox-exporter
  template:
    metadata:
      labels:
        app: blackbox-exporter
      annotations:
        # Probe TLS endpoints directly, as an outside client sees them.
        sidecar.istio.io/inject: "false"
    spec:
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 65534
        runAsGroup: 65534
      containers:
      - name: blackbox-exporter
        image: quay.io/prometheus/blackbox-exporter:%s
        args:
        - --config.file=/etc/blackbox/blackbox.yml
        ports:
        - name: http
          containerPort: 9115
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        volumeMounts:
        - name: config
          mountPath: /etc/blackbox
        readinessProbe:
          httpGet:
            path: /-/ready
            port: http
        resources:
          requests:
            memory: 32Mi
            cpu: 10m
          limits:
            memory: 64Mi
            cpu: 100m
      volumes:
      - name: config
        configMap:
          name: blackbox-exporter
---
apiVersion: v1
kind: Service
metadata:
  name: blackbox-exporter
  namespace: monitoring
  labels:
    app: blackbox-exporter
spec:
  selector:
    app: blackbox-exporter
  ports:
  - name: http
    port: 9115
    targetPort: http`
