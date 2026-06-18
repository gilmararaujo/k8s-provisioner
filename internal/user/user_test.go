package user

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestCertIssuer_CreateCSR verifies the pure-crypto CSR carries the username as
// CommonName and groups as Organizations.
func TestCertIssuer_CreateCSR(t *testing.T) {
	c := newCertIssuer(fake.NewClientset())

	key, err := c.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	csrPEM, err := c.CreateCSR(key, "alice", []string{"devs", "qa"})
	if err != nil {
		t.Fatalf("CreateCSR: %v", err)
	}

	block, _ := pem.Decode(csrPEM)
	if block == nil {
		t.Fatal("CreateCSR did not produce a PEM block")
	}
	req, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}
	if req.Subject.CommonName != "alice" {
		t.Errorf("CommonName = %q, want alice", req.Subject.CommonName)
	}
	// x509 may reorder multi-valued RDNs, so compare as a set.
	gotOrgs := map[string]bool{}
	for _, o := range req.Subject.Organization {
		gotOrgs[o] = true
	}
	if len(gotOrgs) != 2 || !gotOrgs["devs"] || !gotOrgs["qa"] {
		t.Errorf("Organization = %v, want {devs, qa}", req.Subject.Organization)
	}
}

// TestRBACBinder_BindClusterRole verifies the binding is created against the
// injected (fake) clientset with the expected subjects — the DIP payoff.
func TestRBACBinder_BindClusterRole(t *testing.T) {
	cs := fake.NewClientset()
	b := newRBACBinder(cs)

	if err := b.BindClusterRole("alice", []string{"devs"}, "view"); err != nil {
		t.Fatalf("BindClusterRole: %v", err)
	}

	got, err := cs.RbacV1().ClusterRoleBindings().Get(context.TODO(), "alice-view-binding", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected binding created: %v", err)
	}
	if got.RoleRef.Name != "view" {
		t.Errorf("RoleRef.Name = %q, want view", got.RoleRef.Name)
	}
	// One User subject + one Group subject.
	if len(got.Subjects) != 2 {
		t.Fatalf("subjects = %d, want 2 (%+v)", len(got.Subjects), got.Subjects)
	}
	if got.Subjects[0].Kind != "User" || got.Subjects[0].Name != "alice" {
		t.Errorf("subject[0] = %+v, want User/alice", got.Subjects[0])
	}
	if got.Subjects[1].Kind != "Group" || got.Subjects[1].Name != "devs" {
		t.Errorf("subject[1] = %+v, want Group/devs", got.Subjects[1])
	}
}

// TestRBACBinder_CreateRole_Idempotent verifies a duplicate Role create is a
// no-op (the "already exists" branch).
func TestRBACBinder_CreateRole_Idempotent(t *testing.T) {
	b := newRBACBinder(fake.NewClientset())
	rules := GetDefaultDeveloperRules()

	if err := b.CreateRole("developer", "dev", rules); err != nil {
		t.Fatalf("first CreateRole: %v", err)
	}
	if err := b.CreateRole("developer", "dev", rules); err != nil {
		t.Fatalf("duplicate CreateRole should be a no-op, got: %v", err)
	}
}

// TestRBACBinder_DeleteForUser removes only the target user's bindings.
func TestRBACBinder_DeleteForUser(t *testing.T) {
	cs := fake.NewClientset(
		&rbac.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "alice-view-binding"}},
		&rbac.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "bob-view-binding"}},
	)
	b := newRBACBinder(cs)

	if err := b.DeleteForUser("alice"); err != nil {
		t.Fatalf("DeleteForUser: %v", err)
	}

	if _, err := cs.RbacV1().ClusterRoleBindings().Get(context.TODO(), "alice-view-binding", metav1.GetOptions{}); err == nil {
		t.Error("alice's binding should have been deleted")
	}
	if _, err := cs.RbacV1().ClusterRoleBindings().Get(context.TODO(), "bob-view-binding", metav1.GetOptions{}); err != nil {
		t.Errorf("bob's binding should remain: %v", err)
	}
}

// TestManager_DeleteUser_RemovesLocalArtifacts verifies the orchestrator wires
// the store: a DI'd Manager removes the user's directory.
func TestManager_DeleteUser_RemovesLocalArtifacts(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(fake.NewClientset(), filepath.Join(dir, "admin.kubeconfig"), dir)

	userDir := filepath.Join(dir, "alice")
	if err := os.MkdirAll(userDir, 0750); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := m.DeleteUser("alice"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := os.Stat(userDir); !os.IsNotExist(err) {
		t.Errorf("user dir should be removed, stat err = %v", err)
	}
}
