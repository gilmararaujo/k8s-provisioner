package user

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/client-go/kubernetes/fake"
)

// TestRollbackUser_RemovesLocalArtifacts covers the EH-15 rollback path that
// CreateUser defers on partial failure: the user's local directory is removed.
// The in-cluster CSR delete is best-effort/non-fatal, so a missing CSR is fine.
func TestRollbackUser_RemovesLocalArtifacts(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(fake.NewClientset(), filepath.Join(dir, "admin.kubeconfig"), dir)

	userDir := filepath.Join(dir, "bob")
	if err := os.MkdirAll(userDir, 0750); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "bob.key"), []byte("x"), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m.rollbackUser("bob")

	if _, err := os.Stat(userDir); !os.IsNotExist(err) {
		t.Errorf("rollbackUser should remove the user dir, stat err = %v", err)
	}
}
