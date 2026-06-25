package provisioner

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
	"github.com/techiescamp/k8s-provisioner/internal/installer"
)

// Timeout constants for provisioner operations
const (
	nodeReadyTimeout      = 5 * time.Minute
	apiServerReadyTimeout = 5 * time.Minute
	defaultPollInterval   = 10 * time.Second
)

type Provisioner struct {
	config  *config.Config
	exec    executor.CommandExecutor
	verbose bool
	dryRun  bool
}

// New builds a Provisioner with the production executor.
func New(cfg *config.Config, verbose bool) *Provisioner {
	return NewWithExecutor(cfg, executor.New(verbose), verbose)
}

// NewDryRun builds a Provisioner that previews commands without mutating the
// host. Shell/command calls are printed via the Null-Object executor, file
// writes are skipped, readiness waits short-circuit, and InstallWorkloads prints
// the component plan instead of running installers.
func NewDryRun(cfg *config.Config, verbose bool) *Provisioner {
	p := NewWithExecutor(cfg, executor.DryRunExecutor{}, verbose)
	p.dryRun = true
	return p
}

// NewWithExecutor builds a Provisioner with an injected executor. Tests pass a
// mock CommandExecutor here to assert orchestration without touching the host.
func NewWithExecutor(cfg *config.Config, exec executor.CommandExecutor, verbose bool) *Provisioner {
	return &Provisioner{
		config:  cfg,
		exec:    exec,
		verbose: verbose,
	}
}

// auditPolicy is the kube-apiserver audit policy mounted into the control plane.
// First match wins: drop read/health noise and high-frequency coordination
// heartbeats (leases/events/endpoints/endpointslices — huge volume, low value),
// capture security-sensitive objects at full fidelity, exec/attach at Request, and
// every other mutation at Metadata. Reads of secrets are intentionally not logged
// (matched by the get/list/watch=None rule first) to bound volume; reorder if
// secret reads must be audited.
const auditPolicy = `apiVersion: audit.k8s.io/v1
kind: Policy
omitStages:
  - RequestReceived
rules:
  - level: None
    verbs: ["get", "list", "watch"]
  - level: None
    nonResourceURLs: ["/healthz*", "/livez*", "/readyz*", "/version", "/metrics"]
  - level: None
    resources:
      - group: "coordination.k8s.io"
        resources: ["leases"]
      - group: ""
        resources: ["events", "endpoints"]
      - group: "discovery.k8s.io"
        resources: ["endpointslices"]
  - level: RequestResponse
    resources:
      - group: ""
        resources: ["secrets", "serviceaccounts"]
      - group: "rbac.authorization.k8s.io"
        resources: ["roles", "rolebindings", "clusterroles", "clusterrolebindings"]
  - level: Request
    resources:
      - group: ""
        resources: ["pods/exec", "pods/attach", "pods/portforward"]
  - level: Metadata
`

// kubeadmConfigTemplate renders InitConfiguration + ClusterConfiguration used in
// place of bare `kubeadm init` flags, so API server audit logging is wired in
// cleanly via extraArgs/extraVolumes — kubeadm injects the flags, volume and mount
// into the static pod manifest idempotently (a sed patch would be far more fragile
// and could crashloop the API server). %s = advertise address, %s = pod subnet.
//
// audit-log-path is "-" (stdout): audit events become part of the kube-apiserver
// container's stdout, so the existing (non-root, hardened) Alloy DaemonSet collects
// them via the Kubernetes API — the same path as every other pod log — and ships
// them to Loki. No file on disk, no privileged log shipper, no host mount for logs.
// Only the read-only audit-policy volume is needed. (Rotation flags are file-only
// and therefore omitted; the container runtime handles stdout log rotation.)
const kubeadmConfigTemplate = `apiVersion: kubeadm.k8s.io/v1beta4
kind: InitConfiguration
localAPIEndpoint:
  advertiseAddress: %s
nodeRegistration:
  criSocket: unix:///var/run/crio/crio.sock
  name: controlplane
---
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
networking:
  podSubnet: %s
apiServer:
  extraArgs:
  - name: audit-policy-file
    value: /etc/kubernetes/audit/policy.yaml
  - name: audit-log-path
    value: "-"
  extraVolumes:
  - name: audit-policy
    hostPath: /etc/kubernetes/audit
    mountPath: /etc/kubernetes/audit
    readOnly: true
    pathType: DirectoryOrCreate
`

// writeKubeadmConfig writes the API server audit policy and the kubeadm config to
// the control-plane node, returning the config path for `kubeadm init --config`.
// The policy must exist before the API server static pod starts, so it is written
// first. No-op (returns the path) under dry-run.
func (p *Provisioner) writeKubeadmConfig() (string, error) {
	const (
		auditDir   = "/etc/kubernetes/audit"
		policyPath = auditDir + "/policy.yaml"
		configPath = "/etc/kubernetes/kubeadm-config.yaml"
	)

	if p.dryRun {
		fmt.Println("[dry-run] would write kubeadm config + API server audit policy")
		return configPath, nil
	}

	if _, err := p.exec.RunShell("mkdir -p " + auditDir); err != nil {
		return "", fmt.Errorf("create audit policy directory: %w", err)
	}
	if err := executor.WriteFile(policyPath, auditPolicy); err != nil {
		return "", fmt.Errorf("write audit policy: %w", err)
	}
	config := fmt.Sprintf(kubeadmConfigTemplate, p.config.Network.ControlPlaneIP, p.config.Cluster.PodCIDR)
	if err := executor.WriteFile(configPath, config); err != nil {
		return "", fmt.Errorf("write kubeadm config: %w", err)
	}
	return configPath, nil
}

// InitCluster bootstraps the Kubernetes control plane and generates the join
// command for worker nodes. It does NOT install any workloads — call
// InstallWorkloads after all workers have joined.
func (p *Provisioner) InitCluster() error {
	cfg := p.config

	configPath, err := p.writeKubeadmConfig()
	if err != nil {
		return fmt.Errorf("prepare kubeadm config: %w", err)
	}

	fmt.Println("\n>>> Initializing Kubernetes cluster (with API server audit logging)...")
	if err := p.exec.RunShellWithOutput("kubeadm init --config=" + configPath); err != nil {
		return err
	}

	fmt.Println("\n>>> Configuring kubectl...")
	cmds := []string{
		"mkdir -p /home/vagrant/.kube",
		"cp /etc/kubernetes/admin.conf /home/vagrant/.kube/config",
		"chown -R vagrant:vagrant /home/vagrant/.kube",
		"mkdir -p /root/.kube",
		"cp /etc/kubernetes/admin.conf /root/.kube/config",
	}
	for _, cmd := range cmds {
		if _, err := p.exec.RunShell(cmd); err != nil {
			return err
		}
	}

	fmt.Println("\n>>> Removing control-plane taint...")
	_, _ = p.exec.RunShell("kubectl taint nodes controlplane node-role.kubernetes.io/control-plane:NoSchedule- 2>/dev/null || true")

	fmt.Println("\n>>> Patching CoreDNS upstream DNS...")
	if err := p.patchCoreDNS(); err != nil {
		fmt.Printf("Warning: CoreDNS patch failed: %v\n", err)
	}

	fmt.Println("\n>>> Installing Calico CNI...")
	if p.dryRun {
		fmt.Println("[dry-run] would install Calico CNI")
	} else {
		calicoInstaller := installer.NewCalico(cfg, p.exec)
		if err := calicoInstaller.Install(); err != nil {
			return err
		}
	}

	fmt.Println("\n>>> Waiting for node to be ready...")
	if err := p.waitForNode("controlplane", nodeReadyTimeout); err != nil {
		return err
	}

	fmt.Println("\n>>> Generating join command...")
	if _, err := p.exec.RunShell("kubeadm token create --print-join-command > /vagrant/join-command.sh"); err != nil {
		return err
	}
	if _, err := p.exec.RunShell("chmod +x /vagrant/join-command.sh"); err != nil {
		return err
	}

	fmt.Println("\n>>> Control plane ready. Workers can now join the cluster.")
	return nil
}

// workloadStep declares one component in the install sequence. Ordering,
// enablement, and failure policy are data here instead of control flow, so the
// sequence can be read, reordered, and unit-tested in one place.
type workloadStep struct {
	// enabled gates the step; nil means always install.
	enabled func(*config.Config) bool
	// build constructs the installer for this step.
	build func(*config.Config, executor.CommandExecutor) installer.Installer
	// fatal: true aborts the run on failure; false logs a warning and continues.
	fatal bool
	// post runs after a successful (or warned) install, for side effects that
	// must happen immediately after this component (not at the end of the run).
	post func(*Provisioner) error
}

// workloadSteps is the ordered install plan executed by InstallWorkloads.
// Dependency order: networking → mesh → certs → metrics/autoscaling → storage →
// secrets → observability → identity → AI.
func (p *Provisioner) workloadSteps() []workloadStep {
	enabledMonitoring := func(c *config.Config) bool { return c.Components.Monitoring == "prometheus-stack" }

	return []workloadStep{
		{build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewMetalLB(c, e)
		}, fatal: true},
		{build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewIstio(c, e)
		}, fatal: true},
		// cert-manager: TLS certificates for all *.local services.
		{build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewCertManager(c, e)
		}},
		{build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewMetricsServer(c, e)
		}, fatal: true},
		{
			enabled: func(c *config.Config) bool { return c.Components.VPA == "enabled" },
			build:   func(c *config.Config, e executor.CommandExecutor) installer.Installer { return installer.NewVPA(c, e) },
		},
		{
			enabled: func(c *config.Config) bool { return c.Components.KEDA == "enabled" },
			build:   func(c *config.Config, e executor.CommandExecutor) installer.Installer { return installer.NewKEDA(c, e) },
		},
		// NFS provisioner: provides nfs-dynamic and nfs-static StorageClasses.
		{build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewNFSProvisioner(c, e)
		}, fatal: true},
		// Vault runs on the storage node (secrets management).
		{build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewVaultInstaller(c, e)
		}},
		// VSO syncs Vault secrets into K8s Secrets before components start.
		{build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewVaultSecretsOperator(c, e)
		}},
		{enabled: enabledMonitoring, build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewMonitoring(c, e)
		}, fatal: true},
		{enabled: enabledMonitoring, build: func(c *config.Config, e executor.CommandExecutor) installer.Installer { return installer.NewLoki(c, e) }, fatal: true},
		{
			// Tracing requires the monitoring stack and otel-tempo enabled.
			enabled: func(c *config.Config) bool { return enabledMonitoring(c) && c.Components.Tracing == "otel-tempo" },
			build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
				return installer.NewTempo(c, e)
			},
		},
		// Kiali: service mesh observability — requires Prometheus.
		{enabled: enabledMonitoring, build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
			return installer.NewKiali(c, e)
		}},
		{
			// Keycloak after monitoring so Grafana OAuth2 can be configured later.
			enabled: func(c *config.Config) bool { return c.Components.Keycloak == "enabled" },
			build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
				return installer.NewKeycloak(c, e)
			},
			post: (*Provisioner).refreshCalicoAfterKeycloak,
		},
		{
			// Ollama before Karpor when AI is enabled with the ollama backend.
			enabled: func(c *config.Config) bool {
				return c.Components.Karpor == "enabled" && c.KarporAI.Enabled && c.KarporAI.Backend == "ollama"
			},
			build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
				return installer.NewOllama(c, e)
			},
			fatal: true,
		},
		{
			enabled: func(c *config.Config) bool { return c.Components.Karpor == "enabled" },
			build: func(c *config.Config, e executor.CommandExecutor) installer.Installer {
				return installer.NewKarpor(c, e)
			},
			fatal: true,
		},
	}
}

// InstallWorkloads installs all cluster workloads on an already-running cluster
// where all worker nodes have joined. Called after InitCluster + JoinWorker.
func (p *Provisioner) InstallWorkloads() error {
	cfg := p.config

	if p.dryRun {
		return p.printWorkloadPlan()
	}

	// Keep the Keycloak installer to configure Grafana OAuth2 after every other
	// component is up (Grafana must already exist).
	var keycloak *installer.Keycloak

	for _, step := range p.workloadSteps() {
		if step.enabled != nil && !step.enabled(cfg) {
			continue
		}

		inst := step.build(cfg, p.exec)
		fmt.Printf("\n>>> Installing %s...\n", inst.Name())
		if err := inst.Install(); err != nil {
			if step.fatal {
				return fmt.Errorf("%s installation failed: %w", inst.Name(), err)
			}
			fmt.Printf("Warning: %s installation failed: %v\n", inst.Name(), err)
		}

		if kc, ok := inst.(*installer.Keycloak); ok {
			keycloak = kc
		}

		if step.post != nil {
			if err := step.post(p); err != nil {
				if step.fatal {
					return fmt.Errorf("%s post-install failed: %w", inst.Name(), err)
				}
				fmt.Printf("Warning: %s post-install failed: %v\n", inst.Name(), err)
			}
		}
	}

	// Configure Grafana OAuth2 with Keycloak after all components are installed.
	if keycloak != nil {
		fmt.Println("\n>>> Configuring Grafana OAuth2 with Keycloak...")
		if err := keycloak.ConfigureGrafanaOAuth(); err != nil {
			fmt.Printf("Warning: Grafana OAuth2 configuration failed: %v\n", err)
		}
	}

	p.printSuccess()
	return nil
}

// printWorkloadPlan lists the components that would be installed (respecting
// enablement) and their failure policy, without running any installer. Used by
// the dry-run path, where executing installers would block on readiness waits.
func (p *Provisioner) printWorkloadPlan() error {
	fmt.Println("\n[dry-run] Workload install plan:")
	for _, step := range p.workloadSteps() {
		if step.enabled != nil && !step.enabled(p.config) {
			continue
		}
		policy := "warn-on-failure"
		if step.fatal {
			policy = "fatal-on-failure"
		}
		fmt.Printf("  - %s (%s)\n", step.build(p.config, p.exec).Name(), policy)
	}
	if p.config.Components.Keycloak == "enabled" {
		fmt.Println("  - (post) configure Grafana OAuth2 with Keycloak")
	}
	return nil
}

// refreshCalicoAfterKeycloak restarts calico-node after Keycloak installs the
// AuthenticationConfiguration that restarts the API server. That restart
// invalidates the CNI kubeconfig token calico-node wrote at install time, so
// each node needs a fresh token before workers join. Failures are non-fatal.
func (p *Provisioner) refreshCalicoAfterKeycloak() error {
	fmt.Println("\n>>> Refreshing Calico CNI kubeconfig after API server restart...")
	if _, err := p.exec.RunShell("kubectl rollout restart daemonset/calico-node -n calico-system"); err != nil {
		fmt.Printf("Warning: calico-node restart failed: %v\n", err)
		return nil
	}
	if _, err := p.exec.RunShell("kubectl rollout status daemonset/calico-node -n calico-system --timeout=3m"); err != nil {
		fmt.Printf("Warning: calico-node rollout status: %v\n", err)
	}
	return nil
}

// InitControlPlane is kept for backward compatibility and runs InitCluster + InstallWorkloads.
func (p *Provisioner) InitControlPlane() error {
	if err := p.InitCluster(); err != nil {
		return err
	}
	return p.InstallWorkloads()
}

func (p *Provisioner) JoinWorker() error {
	cfg := p.config

	// Wait for join command file or API server
	fmt.Println("\n>>> Waiting for control plane...")
	if err := p.waitForAPIServer(cfg.Network.ControlPlaneIP, apiServerReadyTimeout); err != nil {
		return err
	}

	// Try to use join command file first
	if executor.FileExists("/vagrant/join-command.sh") {
		fmt.Println("\n>>> Using join command from shared file...")
		return p.exec.RunShellWithOutput("bash /vagrant/join-command.sh")
	}

	// Fallback: get join command via SSH
	fmt.Println("\n>>> Getting join command via SSH...")
	if _, err := p.exec.RunShell("apt-get install -y sshpass"); err != nil {
		return err
	}

	joinCmd := fmt.Sprintf("sshpass -p 'vagrant' ssh -o StrictHostKeyChecking=no vagrant@%s 'sudo kubeadm token create --print-join-command'",
		cfg.Network.ControlPlaneIP)

	out, err := p.exec.RunShell(joinCmd)
	if err != nil {
		return err
	}

	return p.exec.RunShellWithOutput(out)
}

func (p *Provisioner) waitForNode(name string, timeout time.Duration) error {
	if p.dryRun {
		fmt.Printf("[dry-run] skip waiting for node %s\n", name)
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := p.exec.RunShell(fmt.Sprintf("kubectl get node %s -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}'", name))
		if err == nil && out == "True" {
			return nil
		}
		time.Sleep(defaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for node %s", name)
}

func (p *Provisioner) waitForAPIServer(ip string, timeout time.Duration) error {
	if p.dryRun {
		fmt.Printf("[dry-run] skip waiting for API server at %s:6443\n", ip)
		return nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := p.exec.RunShell(fmt.Sprintf("nc -z %s 6443", ip))
		if err == nil {
			return nil
		}
		fmt.Printf("Waiting for API server at %s:6443...\n", ip)
		time.Sleep(defaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for API server at %s:6443", ip)
}

func (p *Provisioner) patchCoreDNS() error {
	patch := `'[{"op":"replace","path":"/data/Corefile","value":".:53 {\n    errors\n    health {\n       lameduck 5s\n    }\n    ready\n    kubernetes cluster.local in-addr.arpa ip6.arpa {\n       pods insecure\n       fallthrough in-addr.arpa ip6.arpa\n       ttl 30\n    }\n    prometheus :9153\n    forward . 8.8.8.8 1.1.1.1\n    cache 30\n    loop\n    reload\n    loadbalance\n}\n"}]'`
	_, err := p.exec.RunShell(fmt.Sprintf("kubectl patch configmap coredns -n kube-system --type=json -p %s", patch))
	return err
}

func (p *Provisioner) printSuccess() {
	cfg := p.config
	fmt.Println("\n" + strings.Repeat("=", 50))
	fmt.Println("   Control plane configured successfully!")
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("\nTo access the cluster from MacBook:")
	fmt.Println("\n  1. Copy kubeconfig:")
	fmt.Printf("     vagrant ssh controlplane -c 'sudo cat /etc/kubernetes/admin.conf' > ~/.kube/config-lab\n")
	fmt.Println("\n  2. Adjust server IP:")
	fmt.Printf("     sed -i '' 's/127.0.0.1/%s/' ~/.kube/config-lab\n", cfg.Network.ControlPlaneIP)
	fmt.Println("\n  3. Use the config:")
	fmt.Println("     export KUBECONFIG=~/.kube/config-lab")
	fmt.Println("\n  4. Test:")
	fmt.Println("     kubectl get nodes")
	fmt.Printf("\nMetalLB IP Range: %s\n", cfg.Network.MetalLBRange)
}
