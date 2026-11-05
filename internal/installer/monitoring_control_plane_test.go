package installer

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestControlPlaneScrapes_SelectStaticPods(t *testing.T) {
	type doc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Selector map[string]any `yaml:"selector"` // a ServiceMonitor's has matchLabels
			Ports    []struct {
				Name string `yaml:"name"`
				Port int    `yaml:"port"`
			} `yaml:"ports"`
		} `yaml:"spec"`
	}
	services := map[string]doc{}
	dec := yaml.NewDecoder(bytes.NewBufferString(controlPlaneScrapes))
	for {
		var d doc
		err := dec.Decode(&d)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		if d.Kind == "Service" {
			services[d.Metadata.Name] = d
		}
	}

	for name, want := range map[string]struct {
		component string
		port      int
	}{
		controllerManagerJob: {"kube-controller-manager", 10257},
		schedulerJob:         {"kube-scheduler", 10259},
		etcdJob:              {"etcd", 2381},
	} {
		svc, ok := services[name]
		require.True(t, ok, "Service %s (its name is the job label)", name)
		assert.Equal(t, map[string]any{"component": want.component, "tier": "control-plane"}, svc.Spec.Selector,
			"selects the kubeadm static pods on every control plane node")
		require.Len(t, svc.Spec.Ports, 1)
		assert.Equal(t, want.port, svc.Spec.Ports[0].Port)
	}
	assert.Contains(t, controlPlaneScrapes, "caFile: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt\n      serverName: kubernetes",
		"the apiserver's cluster-CA certificate is verified")
	assert.Contains(t, controlPlaneScrapes, `apiserver_request_duration_seconds_bucket;(1|1\.0|\+Inf)`,
		"only the buckets the slow-request alert reads are kept")
}

func TestControlPlaneRules(t *testing.T) {
	out, err := controlPlaneRules()
	require.NoError(t, err)
	var rule struct {
		Spec struct {
			Groups []promRuleGroup `yaml:"groups"`
		} `yaml:"spec"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(out), &rule))
	require.Len(t, rule.Spec.Groups, 1)

	exprs := map[string]string{}
	for _, r := range rule.Spec.Groups[0].Rules {
		exprs[r.Alert] = r.Expr
		assert.NotEmpty(t, r.Labels["severity"], r.Alert)
		assert.NotEmpty(t, r.Annotations["summary"], r.Alert)
	}
	for alert, job := range map[string]string{
		"KubeAPIServerDown":         apiServerJob,
		"KubeControllerManagerDown": controllerManagerJob,
		"KubeSchedulerDown":         schedulerJob,
		"EtcdDown":                  etcdJob,
	} {
		assert.Equal(t, `(max(up{job="`+job+`"}) or vector(0)) == 0`, exprs[alert], "absent counts as down")
	}
	for _, alert := range []string{"KubeAPIErrorsHigh", "KubeAPISlowRequests", "EtcdNoLeader", "EtcdSlowDisk",
		"EtcdProposalsFailing", "KubeControlPlaneNoLeader", "KubePodsUnschedulable"} {
		assert.Contains(t, exprs, alert)
	}
	assert.Contains(t, exprs["KubeAPISlowRequests"], `le=~"1|1.0"`, "reads the bucket the scrape keeps, in either spelling")
}

func TestInstallControlPlaneMonitoring_AppliesScrapesAndRules(t *testing.T) {
	sh := &fakeShell{}
	require.NoError(t, NewMonitoring(&config.Config{}, sh).installControlPlaneMonitoring())
	require.Len(t, sh.stdins, 2)
	assert.Equal(t, controlPlaneScrapes, sh.stdins[0])
	assert.True(t, strings.Contains(sh.stdins[1], "kind: PrometheusRule"))
}
