package installer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func externalDNSConfig() *config.Config {
	cfg := &config.Config{Nodes: []config.NodeConfig{{Name: "storage", Role: "storage", IP: "192.168.56.20"}}}
	cfg.Cluster.Name = "k8s-lab"
	cfg.Components.ExternalDNS = "enabled"
	return cfg
}

func TestExternalDNSManifest_TargetsStorageEtcd(t *testing.T) {
	m := NewExternalDNS(externalDNSConfig(), &fakeShell{}).manifest()

	assert.Contains(t, m, "--provider=coredns")
	assert.Contains(t, m, "--source=istio-gateway")
	assert.Contains(t, m, "--domain-filter=lab.internal")
	assert.Contains(t, m, "--txt-owner-id=k8s-lab")
	assert.Contains(t, m, "value: https://192.168.56.20:2379")
	assert.Contains(t, m, "external-dns/external-dns:v0.23.0", "falls back to the pinned default version")
}

func TestExternalDNSEtcdSecret_CopiesClientCertFromStorage(t *testing.T) {
	pem := "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"
	sh := &fakeShell{outputs: map[string]string{"sudo cat " + etcdTLSDirRemote: pem}}

	require.NoError(t, NewExternalDNS(externalDNSConfig(), sh).createEtcdSecret())

	var fetched int
	for _, c := range sh.calls {
		if strings.Contains(c, "@192.168.56.20 'sudo cat "+etcdTLSDirRemote) {
			fetched++
		}
	}
	assert.Equal(t, 3, fetched, "CA, client cert and client key are fetched from the storage node")
}

func TestExternalDNSEtcdSecret_RejectsNonPEM(t *testing.T) {
	sh := &fakeShell{outputs: map[string]string{"sudo cat " + etcdTLSDirRemote: "cat: No such file"}}

	err := NewExternalDNS(externalDNSConfig(), sh).createEtcdSecret()
	assert.ErrorContains(t, err, "is not PEM")
}

func TestEnsureGatewayCert_ReturnsSecretOnceReady(t *testing.T) {
	sh := &fakeShell{outputs: map[string]string{"kubectl get certificate keycloak-tls": "True"}}

	secret, err := ensureGatewayCert(sh, "keycloak", "keycloak.lab.internal")
	require.NoError(t, err)
	assert.Equal(t, "keycloak-tls", secret, "one secret per Gateway, never a shared wildcard")
	assert.Contains(t, sh.calls, "kubectl apply -f -")
}
