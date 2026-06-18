package user

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	certificates "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const fixtureKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: test
  cluster:
    server: https://example:6443
contexts:
- name: test
  context:
    cluster: test
    user: admin
current-context: test
users:
- name: admin
  user: {}
`

// TestCreateUser_RollsBackOnRBACFailure drives the full CreateUser flow to its
// last step (RBAC) and forces that step to fail, proving the EH-15 rollback defer
// removes the user's local artifacts. The CSR signing is faked via a get-reactor
// so WaitForCertificate returns immediately.
func TestCreateUser_RollsBackOnRBACFailure(t *testing.T) {
	dir := t.TempDir()
	kubeconfigPath := filepath.Join(dir, "admin.kubeconfig")
	require.NoError(t, os.WriteFile(kubeconfigPath, []byte(fixtureKubeconfig), 0600))

	cs := fake.NewClientset()

	// Every CSR get returns a signed certificate, so WaitForCertificate succeeds.
	cs.PrependReactor("get", "certificatesigningrequests",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			name := action.(k8stesting.GetAction).GetName()
			return true, &certificates.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Status: certificates.CertificateSigningRequestStatus{
					Certificate: []byte("-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----"),
				},
			}, nil
		})
	// Fail the ClusterRoleBinding create -> CreateUser must roll back.
	cs.PrependReactor("create", "clusterrolebindings",
		func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, assert.AnError
		})

	m := NewManager(cs, kubeconfigPath, dir)
	err := m.CreateUser(UserConfig{Username: "carol", ClusterRole: "view", Expiration: 365})

	require.Error(t, err, "CreateUser should fail when RBAC fails")
	require.ErrorIs(t, err, assert.AnError, "failure must come from the RBAC step, not earlier")

	userDir := filepath.Join(dir, "carol")
	_, statErr := os.Stat(userDir)
	assert.True(t, os.IsNotExist(statErr), "user dir must be rolled back, stat err = %v", statErr)
}
