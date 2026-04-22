package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

const certManagerVersion = "v1.16.3"

type CertManager struct {
	config *config.Config
	exec   executor.CommandExecutor
}

func NewCertManager(cfg *config.Config, exec executor.CommandExecutor) *CertManager {
	return &CertManager{config: cfg, exec: exec}
}

func (c *CertManager) Install() error {
	fmt.Println("Installing cert-manager...")

	url := fmt.Sprintf("https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml", certManagerVersion)
	if _, err := c.exec.RunShell(fmt.Sprintf("kubectl apply -f %s", url)); err != nil {
		return fmt.Errorf("cert-manager install failed: %w", err)
	}

	fmt.Println("Waiting for cert-manager to be ready...")
	if err := c.waitForReady(DefaultReadyTimeout); err != nil {
		return err
	}

	fmt.Println("Creating self-signed CA issuer...")
	if err := c.createIssuer(); err != nil {
		return fmt.Errorf("issuer creation failed: %w", err)
	}

	fmt.Println("Creating TLS certificates for lab domains...")
	if err := c.createCertificates(); err != nil {
		return fmt.Errorf("certificate creation failed: %w", err)
	}

	fmt.Println("Waiting for certificates to be ready...")
	if err := c.waitForCerts(2 * time.Minute); err != nil {
		fmt.Printf("Warning: certificates may not be ready yet: %v\n", err)
	}

	fmt.Println("Creating cert-manager ServiceMonitor...")
	if err := c.createServiceMonitor(); err != nil {
		fmt.Printf("Warning: ServiceMonitor creation failed: %v\n", err)
	}

	fmt.Println("Creating cert-manager PrometheusRule...")
	if err := c.createPrometheusRule(); err != nil {
		fmt.Printf("Warning: PrometheusRule creation failed: %v\n", err)
	}

	fmt.Println("cert-manager installed successfully!")
	c.printCAInstructions()
	return nil
}

func (c *CertManager) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := c.exec.RunShell(
			"kubectl get pods -n cert-manager -o jsonpath='{.items[*].status.phase}' 2>/dev/null")
		running := 0
		for _, s := range splitWords(out) {
			if s == "Running" {
				running++
			}
		}
		if running >= 3 {
			// cert-manager, cainjector, webhook
			time.Sleep(10 * time.Second) // let webhook register
			return nil
		}
		fmt.Println("Waiting for cert-manager pods...")
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for cert-manager")
}

func (c *CertManager) createIssuer() error {
	manifest := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: lab-ca
  namespace: cert-manager
spec:
  isCA: true
  commonName: k8s-lab-ca
  secretName: lab-ca-secret
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer
    group: cert-manager.io
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: lab-ca-issuer
spec:
  ca:
    secretName: lab-ca-secret`

	if err := executor.WriteFile("/tmp/cert-manager-issuer.yaml", manifest); err != nil {
		return err
	}

	// Wait for CA cert to be issued
	for i := 0; i < 12; i++ {
		if _, err := c.exec.RunShell("kubectl apply -f /tmp/cert-manager-issuer.yaml 2>&1"); err == nil {
			break
		}
		fmt.Println("Waiting for cert-manager CRDs to be ready...")
		time.Sleep(10 * time.Second)
	}

	// Wait for CA secret to exist
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := c.exec.RunShell("kubectl get secret lab-ca-secret -n cert-manager -o jsonpath='{.metadata.name}' 2>/dev/null")
		if out == "lab-ca-secret" {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout waiting for lab CA secret")
}

func (c *CertManager) createCertificates() error {
	manifest := `apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: lab-tls
  namespace: istio-system
spec:
  secretName: lab-tls-secret
  issuerRef:
    name: lab-ca-issuer
    kind: ClusterIssuer
  dnsNames:
  - grafana.local
  - prometheus.local
  - alertmanager.local
  - keycloak.local
  - kiali.local
  - karpor.local`

	if err := executor.WriteFile("/tmp/lab-certs.yaml", manifest); err != nil {
		return err
	}
	_, err := c.exec.RunShell("kubectl apply -f /tmp/lab-certs.yaml")
	return err
}

func (c *CertManager) waitForCerts(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := c.exec.RunShell(
			"kubectl get certificate lab-tls -n istio-system -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' 2>/dev/null")
		if out == "True" {
			return nil
		}
		fmt.Println("Waiting for TLS certificate...")
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for lab-tls certificate")
}

// ExportCA extracts the CA certificate from the cluster and returns it as PEM.
func (c *CertManager) ExportCA() (string, error) {
	return c.exec.RunShell(
		"kubectl get secret lab-ca-secret -n cert-manager -o jsonpath='{.data.tls\\.crt}' 2>/dev/null | base64 -d")
}

func (c *CertManager) printCAInstructions() {
	fmt.Println("\n========================================")
	fmt.Println("  cert-manager — CA Trust Instructions")
	fmt.Println("========================================")
	fmt.Println("\nTo trust the self-signed CA on your Mac:")
	fmt.Println()
	fmt.Println("  vagrant ssh controlplane -c \\")
	fmt.Println("    'kubectl get secret lab-ca-secret -n cert-manager \\")
	fmt.Println("     -o jsonpath={.data.tls\\.crt} | base64 -d' > /tmp/lab-ca.crt")
	fmt.Println()
	fmt.Println("  sudo security add-trusted-cert -d -r trustRoot \\")
	fmt.Println("    -k /Library/Keychains/System.keychain /tmp/lab-ca.crt")
	fmt.Println()
	fmt.Println("After trusting, all *.local services will show a green lock in Safari/Chrome.")
	fmt.Println("========================================")
}

func (c *CertManager) createServiceMonitor() error {
	manifest := `apiVersion: monitoring.coreos.com/v1
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
  - port: tcp-prometheus-servicemonitor
    path: /metrics
    interval: 30s
    scrapeTimeout: 10s`

	if err := executor.WriteFile("/tmp/cert-manager-servicemonitor.yaml", manifest); err != nil {
		return err
	}
	_, err := c.exec.RunShell("kubectl apply -f /tmp/cert-manager-servicemonitor.yaml")
	return err
}

func (c *CertManager) createPrometheusRule() error {
	manifest := `apiVersion: monitoring.coreos.com/v1
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
        summary: "Certificado expirando em breve"
        description: "O certificado {{ $labels.name }} no namespace {{ $labels.namespace }} expira em menos de 30 dias."
    - alert: CertificateExpiryCritical
      expr: certmanager_certificate_expiration_timestamp_seconds - time() < 7 * 24 * 3600
      for: 1h
      labels:
        severity: critical
      annotations:
        summary: "Certificado expirando criticamente"
        description: "O certificado {{ $labels.name }} no namespace {{ $labels.namespace }} expira em menos de 7 dias."
    - alert: CertificateNotReady
      expr: certmanager_certificate_ready_status{condition="True"} != 1
      for: 10m
      labels:
        severity: critical
      annotations:
        summary: "Certificado não está pronto"
        description: "O certificado {{ $labels.name }} no namespace {{ $labels.namespace }} não está no estado Ready."`

	if err := executor.WriteFile("/tmp/cert-manager-prometheusrule.yaml", manifest); err != nil {
		return err
	}
	_, err := c.exec.RunShell("kubectl apply -f /tmp/cert-manager-prometheusrule.yaml")
	return err
}

func splitWords(s string) []string {
	var words []string
	word := ""
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if word != "" {
				words = append(words, word)
				word = ""
			}
		} else {
			word += string(r)
		}
	}
	if word != "" {
		words = append(words, word)
	}
	return words
}