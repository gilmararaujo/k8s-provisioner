package user

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// store manages the on-disk layout of user artifacts (keys, certs, kubeconfigs)
// under a base directory, plus listing. It changes only when the local file
// layout changes (SRP).
type store struct {
	dir string
}

func newStore(dir string) *store {
	return &store{dir: dir}
}

// UserDir returns the directory holding a given user's artifacts.
func (s *store) UserDir(username string) string {
	return filepath.Join(s.dir, username)
}

// Remove deletes a user's local artifact directory. A missing directory is not
// an error; a failed removal is returned so callers can report it.
func (s *store) Remove(username string) error {
	userDir := s.UserDir(username)
	if _, err := os.Stat(userDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat %s: %w", userDir, err)
	}
	fmt.Printf("  Deleting local files: %s\n", userDir)
	if err := os.RemoveAll(userDir); err != nil {
		return fmt.Errorf("removing %s: %w", userDir, err)
	}
	return nil
}

// List prints all users that have a generated kubeconfig under the base dir.
func (s *store) List() error {
	fmt.Println("Users with certificate-based authentication:")
	fmt.Println()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No users found.")
			return nil
		}
		return err
	}

	if len(entries) == 0 {
		fmt.Println("No users found.")
		return nil
	}

	fmt.Printf("%-20s %-40s %-20s\n", "USERNAME", "KUBECONFIG", "GROUPS")
	fmt.Printf("%-20s %-40s %-20s\n", strings.Repeat("-", 20), strings.Repeat("-", 40), strings.Repeat("-", 20))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		username := entry.Name()
		kubeconfigPath := filepath.Join(s.dir, username, username+".kubeconfig")
		certPath := filepath.Join(s.dir, username, username+".crt")

		if _, err := os.Stat(kubeconfigPath); err == nil {
			fmt.Printf("%-20s %-40s %-20s\n", username, kubeconfigPath, parseGroupsFromCert(certPath))
		}
	}

	return nil
}

// parseGroupsFromCert reads the certificate Organizations (the user's groups)
// from a PEM cert file, or "-" if unavailable.
func parseGroupsFromCert(certPath string) string {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return "-"
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "-"
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "-"
	}

	if len(cert.Subject.Organization) > 0 {
		return strings.Join(cert.Subject.Organization, ",")
	}

	return "-"
}
