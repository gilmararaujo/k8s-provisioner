package provisioner

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

var servingUsages = []string{"digital signature", "server auth"}

func labNodes() *config.Config {
	return &config.Config{Nodes: []config.NodeConfig{
		{Name: "storage", Role: "storage", IP: "192.168.56.20"},
		{Name: "controlplane", Role: "controlplane", IP: "192.168.56.10"},
		{Name: "node01", Role: "worker", IP: "192.168.56.11"},
	}}
}

// servingRequest builds the base64 PEM CSR a kubelet would send.
func servingRequest(t *testing.T, cn string, dns []string, ips ...string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: cn, Organization: []string{"system:nodes"}},
		DNSNames: dns,
	}
	for _, ip := range ips {
		tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func TestValidateKubeletServingCSR(t *testing.T) {
	nodes := kubeletNodes(labNodes())
	nodeGroups := []string{"system:nodes", "system:authenticated"}

	name, err := validateKubeletServingCSR("system:node:node01", nodeGroups, servingUsages,
		servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.11"), nodes)
	require.NoError(t, err)
	assert.Equal(t, "node01", name)

	cases := map[string]struct {
		username string
		groups   []string
		usages   []string
		request  string
		wantErr  string
	}{
		"not a node":             {"kubernetes-admin", nodeGroups, servingUsages, servingRequest(t, "kubernetes-admin", nil, "192.168.56.11"), "not a node"},
		"unknown node":           {"system:node:rogue", nodeGroups, servingUsages, servingRequest(t, "system:node:rogue", []string{"rogue"}, "192.168.56.99"), "not in config.yaml"},
		"storage has no kubelet": {"system:node:storage", nodeGroups, servingUsages, servingRequest(t, "system:node:storage", []string{"storage"}, "192.168.56.20"), "not in config.yaml"},
		"missing nodes group":    {"system:node:node01", []string{"system:authenticated"}, servingUsages, servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.11"), "system:nodes"},
		"client usage":           {"system:node:node01", nodeGroups, []string{"digital signature", "client auth"}, servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.11"), "client auth"},
		"CN of another node":     {"system:node:node01", nodeGroups, servingUsages, servingRequest(t, "system:node:controlplane", []string{"node01"}, "192.168.56.11"), "does not match"},
		"another node's name":    {"system:node:node01", nodeGroups, servingUsages, servingRequest(t, "system:node:node01", []string{"controlplane"}, "192.168.56.11"), "DNS SAN"},
		"another node's IP":      {"system:node:node01", nodeGroups, servingUsages, servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.10"), "IP SAN"},
		"extra IP":               {"system:node:node01", nodeGroups, servingUsages, servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.11", "10.0.2.15"), "IP SAN 10.0.2.15"},
		"no IP":                  {"system:node:node01", nodeGroups, servingUsages, servingRequest(t, "system:node:node01", []string{"node01"}), "no IP SAN"},
		"garbage request":        {"system:node:node01", nodeGroups, servingUsages, "bm90IGEgY3Ny", "not PEM"},
	}
	for desc, tc := range cases {
		t.Run(desc, func(t *testing.T) {
			_, err := validateKubeletServingCSR(tc.username, tc.groups, tc.usages, tc.request, nodes)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// csrJSON renders a `kubectl get csr -o json` document.
func csrJSON(t *testing.T, items ...map[string]any) string {
	t.Helper()
	out, err := json.Marshal(map[string]any{"items": items})
	require.NoError(t, err)
	return string(out)
}

func csrItem(name, signer, username, request string, conditions ...string) map[string]any {
	var conds []map[string]string
	for _, c := range conditions {
		conds = append(conds, map[string]string{"type": c})
	}
	return map[string]any{
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"signerName": signer,
			"username":   username,
			"groups":     []string{"system:nodes", "system:authenticated"},
			"usages":     servingUsages,
			"request":    request,
		},
		"status": map[string]any{"conditions": conds},
	}
}

func TestApproveKubeletServingCSRs_ApprovesOnlyValidRequests(t *testing.T) {
	list := csrJSON(t,
		csrItem("csr-cp", kubeletServingSigner, "system:node:controlplane",
			servingRequest(t, "system:node:controlplane", []string{"controlplane"}, "192.168.56.10"), "Approved"),
		csrItem("csr-node01", kubeletServingSigner, "system:node:node01",
			servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.11")),
		csrItem("csr-forged", kubeletServingSigner, "system:node:node01",
			servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.10")),
		csrItem("csr-client", "kubernetes.io/kube-apiserver-client-kubelet", "system:bootstrap:abcdef", ""),
	)
	mock := &mockExecutor{outputs: map[string]string{"get csr": list}}
	p := NewWithExecutor(labNodes(), mock, false)

	require.NoError(t, p.approveKubeletServingCSRs([]string{"controlplane", "node01"}, time.Second))

	var approved []string
	for _, c := range mock.shellCmds {
		if name, ok := strings.CutPrefix(c, "kubectl certificate approve "); ok {
			approved = append(approved, name)
		}
	}
	assert.Equal(t, []string{"csr-node01"}, approved,
		"approve the pending valid CSR only: not the already-approved one, the forged IP, or other signers")
}

func TestApproveKubeletServingCSRs_TimesOutWhenANodeHasNoValidRequest(t *testing.T) {
	list := csrJSON(t, csrItem("csr-forged", kubeletServingSigner, "system:node:node01",
		servingRequest(t, "system:node:node01", []string{"node01"}, "192.168.56.10")))
	p := NewWithExecutor(labNodes(), &mockExecutor{outputs: map[string]string{"get csr": list}}, false)

	err := p.approveKubeletServingCSRs([]string{"node01"}, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "node01")
}

func TestKubeadmConfig_VerifiesKubeletServingCertificates(t *testing.T) {
	assert.Contains(t, kubeadmConfigTemplate, "kind: KubeletConfiguration\nserverTLSBootstrap: true",
		"kubelets must request serving certificates from the cluster CA")
	assert.Contains(t, kubeadmConfigTemplate, "- name: kubelet-certificate-authority\n    value: /etc/kubernetes/pki/ca.crt",
		"the apiserver must verify kubelet serving certificates")
}

func TestKubeletServingBootstrapEnabled(t *testing.T) {
	on := &mockExecutor{outputs: map[string]string{"configmap kubelet-config": "apiVersion: kubelet.config.k8s.io/v1beta1\nserverTLSBootstrap: true\n"}}
	assert.True(t, NewWithExecutor(labNodes(), on, false).kubeletServingBootstrapEnabled())

	older := &mockExecutor{outputs: map[string]string{"configmap kubelet-config": "apiVersion: kubelet.config.k8s.io/v1beta1\nrotateCertificates: true\n"}}
	assert.False(t, NewWithExecutor(labNodes(), older, false).kubeletServingBootstrapEnabled(),
		"a cluster from before serverTLSBootstrap sends no serving CSRs to wait for")
}
