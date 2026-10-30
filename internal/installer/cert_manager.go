package installer

import (
	"fmt"
	"strings"
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
		version = "v1.21.2"
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

	// Gateway certificates are issued by each component installer next to its
	// Gateway (see ensureGatewayCert), not here.

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
  # Users trust this CA on their machines; cert-manager's 90-day default would
  # renew it into a new CA after ~60 days and break that trust. Leaf
  # certificates keep the short default and renew silently under it.
  duration: 87600h # 10 years
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

// ensureGatewayCert issues a certificate from the lab CA covering only the given
// hosts, stored in istio-system/<name>-tls for an Istio Gateway's credentialName,
// and waits until it is Ready. It returns the secret name.
//
// One certificate per Gateway on purpose: browsers reuse an HTTP/2 connection
// for any host its certificate covers, while Istio picks the Gateway by the SNI
// the connection was opened with. A certificate shared across Gateways (e.g. a
// wildcard) therefore sends requests to the wrong Gateway, which answers 404.
func ensureGatewayCert(exec executor.ShellExecutor, name string, hosts ...string) (string, error) {
	secret := name + "-tls"
	var dnsNames strings.Builder
	for _, h := range hosts {
		fmt.Fprintf(&dnsNames, "\n  - %q", h)
	}
	manifest := fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: %[1]s
  namespace: istio-system
spec:
  secretName: %[1]s
  issuerRef:
    name: lab-ca-issuer
    kind: ClusterIssuer
  dnsNames:%[2]s`, secret, dnsNames.String())
	if _, err := exec.RunShellWithStdin("kubectl apply -f -", manifest); err != nil {
		return "", fmt.Errorf("create certificate %s: %w", secret, err)
	}

	deadline := time.Now().Add(certReadyTimeout)
	for time.Now().Before(deadline) {
		out, _ := exec.RunShell(fmt.Sprintf(
			"kubectl get certificate %s -n istio-system -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}' 2>/dev/null", secret))
		if strings.TrimSpace(out) == "True" {
			return secret, nil
		}
		fmt.Printf("Waiting for TLS certificate %s...\n", secret)
		time.Sleep(shortPollInterval)
	}
	return "", fmt.Errorf("timeout waiting for certificate %s", secret)
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
