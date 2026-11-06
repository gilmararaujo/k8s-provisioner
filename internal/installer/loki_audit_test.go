package installer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func auditTestConfig() *config.Config {
	return &config.Config{Nodes: []config.NodeConfig{
		{Name: "storage", Role: "storage", IP: "192.168.56.20"},
		{Name: "controlplane", Role: "controlplane", IP: "192.168.56.10"},
		{Name: "node01", Role: "worker", IP: "192.168.56.11"},
	}}
}

func auditRules(t *testing.T) map[string]promRule {
	t.Helper()
	out, err := auditAlertRules(auditTestConfig())
	require.NoError(t, err)
	var file struct {
		Groups []promRuleGroup `yaml:"groups"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(out), &file))
	require.Len(t, file.Groups, 1)
	rules := map[string]promRule{}
	for _, r := range file.Groups[0].Rules {
		rules[r.Alert] = r
	}
	return rules
}

func TestAuditAlertRules(t *testing.T) {
	rules := auditRules(t)
	assert.Len(t, rules, 6)

	// Requests the provisioner sends from the control plane are not alerts;
	// a burst of 403s is suspicious wherever it comes from.
	fromControlPlane := " !~ `\"sourceIPs\":\\[\"(192\\.168\\.56\\.10)\"`"
	for name, r := range rules {
		assert.True(t, strings.HasPrefix(r.Expr, "sum by ("), name)
		assert.Contains(t, r.Expr, auditSelector, name)
		assert.Contains(t, r.Expr, auditFields, name)
		assert.NotContains(t, r.Expr, "| json |", "%s: a bare json parser labels every nested field", name)
		assert.Contains(t, r.Expr, "[5m]", name)
		assert.Equal(t, "audit", r.Labels["source"], name)
		assert.NotEmpty(t, r.Labels["severity"], name)
		if name == "AuditForbiddenBurst" {
			assert.NotContains(t, r.Expr, fromControlPlane, name)
		} else {
			assert.Contains(t, r.Expr, fromControlPlane, name)
		}
		if name != "AuditAdminCredentialUsed" {
			assert.Contains(t, r.Expr, auditActor, "%s ignores the cluster's own identities", name)
		}
	}
	assert.Contains(t, rules["AuditClusterAdminGranted"].Expr, "`\"name\":\"cluster-admin\"`")
	assert.Contains(t, rules["AuditForbiddenBurst"].Expr, "> 10")
	assert.Contains(t, rules["AuditPrivilegedWorkload"].Expr, "\":true` or `\"hostPath\":\\{`",
		"separate filters: Loki loosens a single alternation into word matches")
	assert.Contains(t, rules["AuditPodExec"].Expr, kialiPortForward, "Kiali's port-forwards to istiod are routine")
}

func TestNotFromControlPlane_NoControlPlaneNoFilter(t *testing.T) {
	assert.Empty(t, notFromControlPlane(&config.Config{}))
}

func TestLokiConfig_RunsTheRuler(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewLoki(auditTestConfig(), sh).installAuditAlerts())
	require.Len(t, sh.stdins, 1)
	assert.Contains(t, sh.stdins[0], "name: "+auditRulesConfigMap)
	assert.Contains(t, sh.stdins[0], "audit.yaml:")
}
