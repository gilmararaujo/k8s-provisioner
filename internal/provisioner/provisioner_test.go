package provisioner

import (
	"strings"
	"testing"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// mockExecutor records shell invocations so orchestration can be asserted
// without touching the host. It satisfies executor.CommandExecutor.
type mockExecutor struct {
	shellCmds []string
}

func (m *mockExecutor) Run(name string, args ...string) (string, error) { return "", nil }
func (m *mockExecutor) RunWithOutput(name string, args ...string) error { return nil }
func (m *mockExecutor) RunShellWithOutput(command string) error {
	m.shellCmds = append(m.shellCmds, command)
	return nil
}
func (m *mockExecutor) RunShellWithStdin(c, s string) (string, error) {
	m.shellCmds = append(m.shellCmds, c)
	return "", nil
}
func (m *mockExecutor) RunShell(command string) (string, error) {
	m.shellCmds = append(m.shellCmds, command)
	return "", nil
}

// planNames returns the ordered names of the steps that would run for cfg.
func planNames(p *Provisioner) []string {
	var names []string
	for _, step := range p.workloadSteps() {
		if step.enabled != nil && !step.enabled(p.config) {
			continue
		}
		names = append(names, step.build(p.config, p.exec).Name())
	}
	return names
}

func TestWorkloadPlan_AllEnabled(t *testing.T) {
	cfg := &config.Config{}
	cfg.Components.Monitoring = "prometheus-stack"
	cfg.Components.Tracing = "otel-tempo"
	cfg.Components.VPA = "enabled"
	cfg.Components.KEDA = "enabled"
	cfg.Components.Keycloak = "enabled"
	cfg.Components.Karpor = "enabled"
	cfg.KarporAI.Enabled = true
	cfg.KarporAI.Backend = "ollama"

	p := NewWithExecutor(cfg, &mockExecutor{}, false)

	want := []string{
		"MetalLB",
		"Istio",
		"cert-manager",
		"Metrics Server",
		"VPA (Vertical Pod Autoscaler)",
		"KEDA (Event-Driven Autoscaling)",
		"NFS Storage Provisioner",
		"Vault (secrets management)",
		"Vault Secrets Operator",
		"Monitoring Stack",
		"Loki Stack",
		"Tracing Stack (Tempo + OpenTelemetry)",
		"Kiali",
		"Keycloak (OIDC)",
		"Ollama",
		"Karpor",
	}

	got := planNames(p)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("plan mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestWorkloadPlan_Minimal(t *testing.T) {
	// Everything optional disabled; only the always-on core should run.
	cfg := &config.Config{}
	p := NewWithExecutor(cfg, &mockExecutor{}, false)

	want := []string{
		"MetalLB",
		"Istio",
		"cert-manager",
		"Metrics Server",
		"NFS Storage Provisioner",
		"Vault (secrets management)",
		"Vault Secrets Operator",
	}

	got := planNames(p)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("plan mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestWorkloadPlan_TracingRequiresMonitoring(t *testing.T) {
	// Tracing enabled but monitoring off → Tempo must NOT be planned.
	cfg := &config.Config{}
	cfg.Components.Tracing = "otel-tempo"
	p := NewWithExecutor(cfg, &mockExecutor{}, false)

	for _, name := range planNames(p) {
		if strings.HasPrefix(name, "Tracing Stack") {
			t.Fatalf("Tempo should not be planned without monitoring; plan: %v", planNames(p))
		}
	}
}

func TestWorkloadPlan_FatalPolicy(t *testing.T) {
	cfg := &config.Config{}
	cfg.Components.Monitoring = "prometheus-stack"
	cfg.Components.Tracing = "otel-tempo"
	cfg.Components.Keycloak = "enabled"
	cfg.Components.Karpor = "enabled"
	p := NewWithExecutor(cfg, &mockExecutor{}, false)

	// Components whose failure must abort the whole run.
	wantFatal := map[string]bool{
		"MetalLB":                 true,
		"Istio":                   true,
		"Metrics Server":          true,
		"NFS Storage Provisioner": true,
		"Monitoring Stack":        true,
		"Loki Stack":              true,
		"Karpor":                  true,
	}

	for _, step := range p.workloadSteps() {
		if step.enabled != nil && !step.enabled(cfg) {
			continue
		}
		name := step.build(cfg, p.exec).Name()
		if step.fatal != wantFatal[name] {
			t.Errorf("%s: fatal=%v, want %v", name, step.fatal, wantFatal[name])
		}
	}
}

func TestInstallWorkloads_DryRunPrintsPlanWithoutExecuting(t *testing.T) {
	cfg := &config.Config{}
	cfg.Components.Monitoring = "prometheus-stack"
	cfg.Components.Keycloak = "enabled"

	mock := &mockExecutor{}
	p := NewWithExecutor(cfg, mock, false)
	p.dryRun = true

	if err := p.InstallWorkloads(); err != nil {
		t.Fatalf("dry-run InstallWorkloads should not error: %v", err)
	}
	// The plan path must not run a single shell command (no installer executes).
	if len(mock.shellCmds) != 0 {
		t.Fatalf("dry-run must not execute commands, got %d: %v", len(mock.shellCmds), mock.shellCmds)
	}
}

func TestWriteFile_DryRunSkips(t *testing.T) {
	p := NewWithExecutor(&config.Config{}, &mockExecutor{}, false)
	p.dryRun = true

	// A path that would fail if actually written (no such directory).
	if err := p.writeFile("/nonexistent-dir/should-not-write.conf", "data"); err != nil {
		t.Fatalf("dry-run writeFile should be a no-op, got: %v", err)
	}
}

// TestRefreshCalicoAfterKeycloak verifies the injected executor is actually used
// — the payoff of NewWithExecutor. The Keycloak post-hook must restart and then
// poll the calico-node daemonset.
func TestRefreshCalicoAfterKeycloak(t *testing.T) {
	mock := &mockExecutor{}
	p := NewWithExecutor(&config.Config{}, mock, false)

	if err := p.refreshCalicoAfterKeycloak(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.shellCmds) != 2 {
		t.Fatalf("expected 2 shell commands, got %d: %v", len(mock.shellCmds), mock.shellCmds)
	}
	if !strings.Contains(mock.shellCmds[0], "rollout restart daemonset/calico-node") {
		t.Errorf("first command should restart calico-node, got: %s", mock.shellCmds[0])
	}
	if !strings.Contains(mock.shellCmds[1], "rollout status daemonset/calico-node") {
		t.Errorf("second command should poll rollout status, got: %s", mock.shellCmds[1])
	}
}
