package provisioner

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// vagrantSyncedDir is the Vagrant synced folder (the repo's vagrant/ directory
// on the host). CA files written under it are directly reachable from the host.
const vagrantSyncedDir = "/vagrant"

// hostCA is a CA the user's machine must trust to open the lab over HTTPS.
type hostCA struct {
	file  string // file name under the export dir
	label string // what it covers
	pem   string
}

// printHostSetup exports the lab CAs where the host can read them and prints the
// one-time commands that make the host resolve the lab domain and trust the CAs.
// Both CAs are regenerated when the lab is recreated, so this runs after every
// install and the trust commands must be re-run after a rebuild.
func (p *Provisioner) printHostSetup() {
	cfg := p.config
	cas := p.collectHostCAs()

	exportDir, hostDir := filepath.Join(vagrantSyncedDir, "certs"), "certs"
	if _, err := os.Stat(vagrantSyncedDir); err != nil {
		exportDir, hostDir = "/etc/k8s-provisioner/certs", "/etc/k8s-provisioner/certs (copy it to your machine)"
	}
	if err := os.MkdirAll(exportDir, 0o755); err != nil { // #nosec G301 -- public CA certificates
		fmt.Printf("Warning: could not create %s: %v\n", exportDir, err)
	}
	for _, ca := range cas {
		if err := p.writeFile(filepath.Join(exportDir, ca.file), ca.pem); err != nil {
			fmt.Printf("Warning: could not export %s: %v\n", ca.file, err)
		}
	}

	dnsIP, domain := cfg.StorageIP(), cfg.Domain()
	fmt.Println("\n" + strings.Repeat("=", 70))
	fmt.Println("   Host setup — run once on your machine, from the vagrant/ directory")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("\nCA certificates exported to %s/:\n", hostDir)
	for _, ca := range cas {
		fmt.Printf("  %-22s SHA-256 %s\n                         %s\n", ca.file, fingerprint(ca.pem), ca.label)
	}

	fmt.Println("\nmacOS:")
	if dnsIP != "" {
		fmt.Printf("  sudo mkdir -p /etc/resolver && echo \"nameserver %s\" | sudo tee /etc/resolver/%s\n", dnsIP, domain)
	}
	for _, ca := range cas {
		fmt.Printf("  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain certs/%s\n", ca.file)
	}
	fmt.Println("  Browsers other than Safari (and terminals/IDEs) need: System Settings → Privacy & Security → Local Network")

	fmt.Println("\nLinux (Debian/Ubuntu with systemd-resolved; vboxnet0 = VirtualBox host-only interface):")
	if dnsIP != "" {
		fmt.Printf("  sudo resolvectl dns vboxnet0 %s && sudo resolvectl domain vboxnet0 '~%s'\n", dnsIP, domain)
	}
	fmt.Println("  sudo cp certs/*.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates")

	fmt.Println("\nWindows (admin PowerShell):")
	if dnsIP != "" {
		fmt.Printf("  Add-DnsClientNrptRule -Namespace \".%s\" -NameServers \"%s\"\n", domain, dnsIP)
	}
	for _, ca := range cas {
		fmt.Printf("  certutil -addstore -f Root certs\\%s\n", ca.file)
	}

	fmt.Printf("\nThen restart the browser and open https://%s\n", cfg.Host("grafana"))
	fmt.Println("Recreated the lab? Both CAs are new: remove the old ones and trust these again (README \"Host setup\").")
}

// collectHostCAs returns the lab CA (cert-manager, signs every Gateway) and the
// Vault CA (storage node), skipping any that is not available.
func (p *Provisioner) collectHostCAs() []hostCA {
	var cas []hostCA
	out, err := p.exec.RunShell("kubectl get secret lab-ca-secret -n cert-manager -o jsonpath='{.data.tls\\.crt}' | base64 -d")
	if err == nil && strings.Contains(out, "BEGIN CERTIFICATE") {
		cas = append(cas, hostCA{"k8s-lab-ca.crt", "lab CA: Grafana, Keycloak, Kiali, ... (*." + p.config.Domain() + ")", strings.TrimSpace(out) + "\n"})
	} else {
		fmt.Printf("Warning: could not read the lab CA from cert-manager: %v\n", err)
	}
	if p.config.Vault.Enabled {
		if data, err := os.ReadFile(p.config.VaultCACert()); err == nil { // #nosec G304 -- configured CA path
			cas = append(cas, hostCA{"k8s-lab-vault-ca.crt", "Vault CA: https://" + p.config.Host("vault") + ":8200", string(data)})
		}
	}
	return cas
}

// fingerprint returns the colon-separated SHA-256 of the first certificate in
// pemData, the form `security`, browsers and openssl display.
func fingerprint(pemData string) string {
	block, _ := pem.Decode([]byte(pemData))
	if block == nil {
		return "(invalid PEM)"
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "(invalid certificate)"
	}
	sum := sha256.Sum256(block.Bytes)
	hexPairs := make([]string, len(sum))
	for i, b := range sum {
		hexPairs[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(hexPairs, ":")
}
