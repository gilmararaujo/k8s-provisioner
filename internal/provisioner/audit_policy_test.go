package provisioner

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type auditRule struct {
	Level           string   `yaml:"level"`
	Users           []string `yaml:"users"`
	UserGroups      []string `yaml:"userGroups"`
	Verbs           []string `yaml:"verbs"`
	NonResourceURLs []string `yaml:"nonResourceURLs"`
	Resources       []struct {
		Group     string   `yaml:"group"`
		Resources []string `yaml:"resources"`
	} `yaml:"resources"`
}

type auditRequest struct {
	user     string
	groups   []string
	verb     string
	group    string
	resource string // "pods" or "pods/exec"
	url      string // non-resource URL; empty for resource requests
}

// matches applies the apiserver's policy rule semantics: every field a rule
// sets must match, and a rule naming resources (or URLs) only matches resource
// (or non-resource) requests.
func (r auditRule) matches(req auditRequest) bool {
	if len(r.Users) > 0 && !slices.Contains(r.Users, req.user) {
		return false
	}
	if len(r.UserGroups) > 0 && !slices.ContainsFunc(r.UserGroups, func(g string) bool { return slices.Contains(req.groups, g) }) {
		return false
	}
	if len(r.Verbs) > 0 && !slices.Contains(r.Verbs, req.verb) {
		return false
	}
	if req.url != "" {
		if len(r.Resources) > 0 {
			return false
		}
		return len(r.NonResourceURLs) == 0 || slices.ContainsFunc(r.NonResourceURLs, func(u string) bool {
			return u == req.url || (strings.HasSuffix(u, "*") && strings.HasPrefix(req.url, strings.TrimSuffix(u, "*")))
		})
	}
	if len(r.NonResourceURLs) > 0 {
		return false
	}
	return len(r.Resources) == 0 || slices.ContainsFunc(r.Resources, func(g struct {
		Group     string   `yaml:"group"`
		Resources []string `yaml:"resources"`
	}) bool {
		return g.Group == req.group && slices.Contains(g.Resources, req.resource)
	})
}

func auditLevel(t *testing.T, req auditRequest) string {
	t.Helper()
	var policy struct {
		Rules []auditRule `yaml:"rules"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(auditPolicy), &policy))
	for _, r := range policy.Rules {
		if r.matches(req) {
			return r.Level
		}
	}
	return "None"
}

func TestAuditPolicy_Levels(t *testing.T) {
	person := []string{"system:authenticated", "oidc:k8s-admins"}
	sa := []string{"system:serviceaccounts", "system:serviceaccounts:monitoring", "system:authenticated"}
	node := []string{"system:nodes", "system:authenticated"}
	admin := []string{"kubeadm:cluster-admins", "system:authenticated"}

	cases := map[string]struct {
		req  auditRequest
		want string
	}{
		"health probe":                {auditRequest{user: "system:anonymous", verb: "get", url: "/readyz"}, "None"},
		"lease heartbeat":             {auditRequest{user: "system:node:node01", groups: node, verb: "update", group: "coordination.k8s.io", resource: "leases"}, "None"},
		"admin reads pods":            {auditRequest{user: "kubernetes-admin", groups: admin, verb: "list", resource: "pods"}, "Metadata"},
		"service account reads":       {auditRequest{user: "system:serviceaccount:monitoring:prometheus", groups: sa, verb: "watch", resource: "pods"}, "None"},
		"node reads secrets":          {auditRequest{user: "system:node:node01", groups: node, verb: "get", resource: "secrets"}, "None"},
		"scheduler reads":             {auditRequest{user: "system:kube-scheduler", groups: []string{"system:authenticated"}, verb: "list", resource: "pods"}, "None"},
		"person reads secrets":        {auditRequest{user: "oidc:alice", groups: person, verb: "get", resource: "secrets"}, "Metadata"},
		"person creates a secret":     {auditRequest{user: "oidc:alice", groups: person, verb: "create", resource: "secrets"}, "Metadata"},
		"controller updates a secret": {auditRequest{user: "system:serviceaccount:cert-manager:cert-manager", groups: sa, verb: "update", resource: "secrets"}, "Metadata"},
		"binding created":             {auditRequest{user: "oidc:alice", groups: person, verb: "create", group: "rbac.authorization.k8s.io", resource: "clusterrolebindings"}, "RequestResponse"},
		"exec":                        {auditRequest{user: "oidc:alice", groups: person, verb: "create", resource: "pods/exec"}, "Request"},
		"pod created":                 {auditRequest{user: "system:serviceaccount:kube-system:replicaset-controller", groups: sa, verb: "create", resource: "pods"}, "Request"},
		"daemonset created":           {auditRequest{user: "oidc:alice", groups: person, verb: "create", group: "apps", resource: "daemonsets"}, "Request"},
		"pod status update":           {auditRequest{user: "system:node:node01", groups: node, verb: "patch", resource: "pods/status"}, "Metadata"},
		"configmap changed":           {auditRequest{user: "oidc:alice", groups: person, verb: "patch", resource: "configmaps"}, "Metadata"},
	}
	for desc, tc := range cases {
		t.Run(desc, func(t *testing.T) {
			assert.Equal(t, tc.want, auditLevel(t, tc.req))
		})
	}
}

// Request or RequestResponse on secrets would copy their values into the audit
// log, and from there into Loki.
func TestAuditPolicy_NeverLogsSecretBodies(t *testing.T) {
	for _, verb := range []string{"get", "list", "watch", "create", "update", "patch", "delete"} {
		for _, groups := range [][]string{{"system:authenticated"}, {"system:serviceaccounts"}, {"kubeadm:cluster-admins"}} {
			for _, user := range []string{"oidc:alice", "kubernetes-admin", "system:serviceaccount:a:b"} {
				level := auditLevel(t, auditRequest{user: user, groups: groups, verb: verb, resource: "secrets"})
				assert.Contains(t, []string{"None", "Metadata"}, level, "%s %s secrets", user, verb)
			}
		}
	}
}

func TestKubeadmConfig_ExposesControlPlaneMetrics(t *testing.T) {
	assert.Contains(t, kubeadmConfigTemplate, "controllerManager:\n  extraArgs:\n  - name: bind-address\n    value: 0.0.0.0")
	assert.Contains(t, kubeadmConfigTemplate, "scheduler:\n  extraArgs:\n  - name: bind-address\n    value: 0.0.0.0")
	assert.Contains(t, kubeadmConfigTemplate, "- name: listen-metrics-urls\n      value: http://0.0.0.0:2381")
}
