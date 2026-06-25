package installer

import (
	"fmt"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type CertManager struct {
	config *config.Config
	exec   executor.ShellExecutor
}

func NewCertManager(cfg *config.Config, exec executor.ShellExecutor) *CertManager {
	return &CertManager{config: cfg, exec: exec}
}

func (c *CertManager) Install() error {
	fmt.Println("Installing cert-manager...")

	version := c.config.Versions.CertManager
	if version == "" {
		version = "v1.16.3"
	}
	url := fmt.Sprintf("https://github.com/cert-manager/cert-manager/releases/download/%s/cert-manager.yaml", version)
	if _, err := c.exec.RunShell(fmt.Sprintf("kubectl apply -f %s", url)); err != nil {
		return fmt.Errorf("cert-manager install failed: %w", err)
	}

	fmt.Println("Waiting for cert-manager to be ready...")
	if err := c.waitForReady(defaultReadyTimeout); err != nil {
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
	if err := c.waitForCerts(certReadyTimeout); err != nil {
		fmt.Printf("Warning: certificates may not be ready yet: %v\n", err)
	}

	// NOTE: the cert-manager ServiceMonitor + PrometheusRule are created by the
	// Monitoring installer (installCertManagerMonitoring), not here. They depend
	// on the Prometheus Operator CRDs (monitoring.coreos.com/v1), which are only
	// installed later in the workload order. Creating them here failed silently.

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
			time.Sleep(webhookRegisterWait)
			return nil
		}
		fmt.Println("Waiting for cert-manager pods...")
		time.Sleep(defaultPollInterval)
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
		time.Sleep(defaultPollInterval)
	}

	// Wait for CA secret to exist
	deadline := time.Now().Add(caSecretWaitTimeout)
	for time.Now().Before(deadline) {
		out, _ := c.exec.RunShell("kubectl get secret lab-ca-secret -n cert-manager -o jsonpath='{.metadata.name}' 2>/dev/null")
		if out == "lab-ca-secret" {
			return nil
		}
		time.Sleep(shortPollInterval)
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
  - karpor.local
  - otel-demo.local`

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
		time.Sleep(defaultPollInterval)
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
	fmt.Println("  # Wrap the remote command in double quotes so the jsonpath stays")
	fmt.Println("  # single-quoted on the node (otherwise the file comes out empty):")
	fmt.Println("  vagrant ssh controlplane -c \\")
	fmt.Println("    \"kubectl get secret lab-ca-secret -n cert-manager -o jsonpath='{.data.tls\\.crt}' | base64 -d\" \\")
	fmt.Println("    > /tmp/lab-ca.crt")
	fmt.Println()
	fmt.Println("  sudo security add-trusted-cert -d -r trustRoot \\")
	fmt.Println("    -k /Library/Keychains/System.keychain /tmp/lab-ca.crt")
	fmt.Println()
	fmt.Println("Then fully quit and reopen the browser. All *.local services will show a green lock.")
	fmt.Println("========================================")
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
