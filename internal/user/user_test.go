package user

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestValidateGroups(t *testing.T) {
	require.NoError(t, validateGroups(nil))
	require.NoError(t, validateGroups([]string{"devs", "qa"}))

	require.Error(t, validateGroups([]string{"system:masters"}),
		"system:masters must be rejected")
	require.Error(t, validateGroups([]string{"devs", "system:authenticated"}),
		"any system: group must be rejected")
}

func TestValidateUsername(t *testing.T) {
	require.NoError(t, validateUsername("joao"))
	require.NoError(t, validateUsername("dev-1"))
	require.NoError(t, validateUsername("a"))

	require.Error(t, validateUsername(""), "empty must be rejected")
	require.Error(t, validateUsername("../../etc/passwd"), "path traversal must be rejected")
	require.Error(t, validateUsername("foo/bar"), "slash must be rejected")
	require.Error(t, validateUsername("UPPER"), "uppercase must be rejected")
	require.Error(t, validateUsername("a.b"), "dot must be rejected")
	require.Error(t, validateUsername("-lead"), "leading dash must be rejected")
}

func TestDeleteUser_RejectsTraversingUsername(t *testing.T) {
	m := NewManager(fake.NewClientset(), "", t.TempDir())
	err := m.DeleteUser("../../../tmp/evil")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid username")
}

func TestCreateUser_RejectsTraversingUsername(t *testing.T) {
	m := NewManager(fake.NewClientset(), "", t.TempDir())
	err := m.CreateUser(UserConfig{Username: "../../../tmp/evil"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid username")
}

func TestSubmit_RejectsOutOfRangeExpiration(t *testing.T) {
	c := newCertIssuer(fake.NewClientset())
	require.Error(t, c.Submit("bob-csr", []byte("x"), 0), "zero must be rejected")
	require.Error(t, c.Submit("bob-csr", []byte("x"), -1), "negative must be rejected")
	require.Error(t, c.Submit("bob-csr", []byte("x"), MaxCertExpirationDays+1), "over max must be rejected")
	// a valid value passes the bound (fake clientset accepts the Create)
	require.NoError(t, c.Submit("bob-csr", []byte("x"), 365))
}

func TestCreateUser_RejectsReservedGroups(t *testing.T) {
	m := NewManager(fake.NewClientset(), "", t.TempDir())
	err := m.CreateUser(UserConfig{Username: "eve", Groups: []string{"system:masters"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reserved group")
}

// TestCertIssuer_CreateCSR verifies the pure-crypto CSR carries the username as
// CommonName and groups as Organizations.
func TestCertIssuer_CreateCSR(t *testing.T) {
	c := newCertIssuer(fake.NewClientset())

	key, err := c.GenerateKey()
	require.NoError(t, err)

	csrPEM, err := c.CreateCSR(key, "alice", []string{"devs", "qa"})
	require.NoError(t, err)

	block, _ := pem.Decode(csrPEM)
	require.NotNil(t, block, "CreateCSR did not produce a PEM block")
	req, err := x509.ParseCertificateRequest(block.Bytes)
	require.NoError(t, err)

	assert.Equal(t, "alice", req.Subject.CommonName)
	// x509 may reorder multi-valued RDNs, so compare as a set.
	assert.ElementsMatch(t, []string{"devs", "qa"}, req.Subject.Organization)
}

// TestRBACBinder_BindClusterRole verifies the binding is created against the
// injected (fake) clientset with the expected subjects — the DIP payoff.
func TestRBACBinder_BindClusterRole(t *testing.T) {
	cs := fake.NewClientset()
	b := newRBACBinder(cs)

	require.NoError(t, b.BindClusterRole("alice", []string{"devs"}, "view"))

	got, err := cs.RbacV1().ClusterRoleBindings().Get(context.TODO(), "alice-view-binding", metav1.GetOptions{})
	require.NoError(t, err, "expected binding created")
	assert.Equal(t, "view", got.RoleRef.Name)
	// One User subject + one Group subject.
	require.Len(t, got.Subjects, 2)
	assert.Equal(t, "User", got.Subjects[0].Kind)
	assert.Equal(t, "alice", got.Subjects[0].Name)
	assert.Equal(t, "Group", got.Subjects[1].Kind)
	assert.Equal(t, "devs", got.Subjects[1].Name)
}

// TestRBACBinder_CreateRole_Idempotent verifies a duplicate Role create is a
// no-op (the "already exists" branch).
func TestRBACBinder_CreateRole_Idempotent(t *testing.T) {
	b := newRBACBinder(fake.NewClientset())
	rules := GetDefaultDeveloperRules()

	require.NoError(t, b.CreateRole("developer", "dev", rules))
	require.NoError(t, b.CreateRole("developer", "dev", rules), "duplicate CreateRole should be a no-op")
}

// TestRBACBinder_DeleteForUser removes only the target user's bindings.
func TestRBACBinder_DeleteForUser(t *testing.T) {
	cs := fake.NewClientset(
		&rbac.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "alice-view-binding"}},
		&rbac.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "bob-view-binding"}},
	)
	b := newRBACBinder(cs)

	require.NoError(t, b.DeleteForUser("alice"))

	_, err := cs.RbacV1().ClusterRoleBindings().Get(context.TODO(), "alice-view-binding", metav1.GetOptions{})
	assert.Error(t, err, "alice's binding should have been deleted")
	_, err = cs.RbacV1().ClusterRoleBindings().Get(context.TODO(), "bob-view-binding", metav1.GetOptions{})
	assert.NoError(t, err, "bob's binding should remain")
}

// TestManager_DeleteUser_RemovesLocalArtifacts verifies the orchestrator wires
// the store: a DI'd Manager removes the user's directory.
func TestManager_DeleteUser_RemovesLocalArtifacts(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(fake.NewClientset(), filepath.Join(dir, "admin.kubeconfig"), dir)

	userDir := filepath.Join(dir, "alice")
	require.NoError(t, os.MkdirAll(userDir, 0750))

	require.NoError(t, m.DeleteUser("alice"))

	_, err := os.Stat(userDir)
	assert.True(t, os.IsNotExist(err), "user dir should be removed, stat err = %v", err)
}
