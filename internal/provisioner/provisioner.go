package provisioner

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
	"github.com/techiescamp/k8s-provisioner/internal/installer"
)

type Provisioner struct {
	config  *config.Config
	exec    *executor.Executor
	verbose bool
}

func New(cfg *config.Config, verbose bool) *Provisioner {
	return &Provisioner{
		config:  cfg,
		exec:    executor.New(verbose),
		verbose: verbose,
	}
}

func (p *Provisioner) InstallCommon() error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"Disabling swap", p.disableSwap},
		{"Loading kernel modules", p.loadKernelModules},
		{"Configuring sysctl", p.configureSysctl},
		{"Installing dependencies", p.installDependencies},
		{"Installing CRI-O", p.installCRIO},
		{"Installing Kubernetes tools", p.installKubernetesTools},
	}

	for _, step := range steps {
		fmt.Printf("\n>>> %s...\n", step.name)
		if err := step.fn(); err != nil {
			return fmt.Errorf("%s failed: %w", step.name, err)
		}
		fmt.Printf("✓ %s completed\n", step.name)
	}

	return nil
}

func (p *Provisioner) disableSwap() error {
	if _, err := p.exec.RunShell("swapoff -a"); err != nil {
		return err
	}
	if _, err := p.exec.RunShell("sed -i '/ swap / s/^/#/' /etc/fstab"); err != nil {
		return err
	}
	return nil
}

func (p *Provisioner) loadKernelModules() error {
	modules := `overlay
br_netfilter`
	if err := executor.WriteFile("/etc/modules-load.d/k8s.conf", modules); err != nil {
		return err
	}

	if _, err := p.exec.Run("modprobe", "overlay"); err != nil {
		return err
	}
	if _, err := p.exec.Run("modprobe", "br_netfilter"); err != nil {
		return err
	}
	return nil
}

func (p *Provisioner) configureSysctl() error {
	sysctl := `net.bridge.bridge-nf-call-iptables  = 1
net.bridge.bridge-nf-call-ip6tables = 1
net.ipv4.ip_forward                 = 1`

	if err := executor.WriteFile("/etc/sysctl.d/k8s.conf", sysctl); err != nil {
		return err
	}

	_, err := p.exec.RunShell("sysctl --system")
	return err
}

func (p *Provisioner) installDependencies() error {
	if _, err := p.exec.RunShell("apt-get update"); err != nil {
		return err
	}

	pkgs := "apt-transport-https ca-certificates curl gnupg software-properties-common conntrack ethtool socat"
	_, err := p.exec.RunShell(fmt.Sprintf("apt-get install -y %s", pkgs))
	return err
}

func (p *Provisioner) installCRIO() error {
	version := p.config.Versions.CriO

	// Add CRI-O repository
	keyCmd := fmt.Sprintf("curl -fsSL https://download.opensuse.org/repositories/isv:/cri-o:/stable:/%s/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/cri-o-apt-keyring.gpg", version)
	if _, err := p.exec.RunShell(keyCmd); err != nil {
		return err
	}

	repoLine := fmt.Sprintf("deb [signed-by=/etc/apt/keyrings/cri-o-apt-keyring.gpg] https://download.opensuse.org/repositories/isv:/cri-o:/stable:/%s/deb/ /", version)
	if err := executor.WriteFile("/etc/apt/sources.list.d/cri-o.list", repoLine); err != nil {
		return err
	}

	if _, err := p.exec.RunShell("apt-get update"); err != nil {
		return err
	}

	if _, err := p.exec.RunShell("apt-get install -y cri-o"); err != nil {
		return err
	}

	if _, err := p.exec.Run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := p.exec.Run("systemctl", "enable", "crio"); err != nil {
		return err
	}
	if _, err := p.exec.Run("systemctl", "start", "crio"); err != nil {
		return err
	}

	return nil
}

func (p *Provisioner) installKubernetesTools() error {
	version := p.config.Versions.Kubernetes

	// Add Kubernetes repository
	keyCmd := fmt.Sprintf("curl -fsSL https://pkgs.k8s.io/core:/stable:/v%s/deb/Release.key | gpg --dearmor -o /etc/apt/keyrings/kubernetes-apt-keyring.gpg", version)
	if _, err := p.exec.RunShell(keyCmd); err != nil {
		return err
	}

	repoLine := fmt.Sprintf("deb [signed-by=/etc/apt/keyrings/kubernetes-apt-keyring.gpg] https://pkgs.k8s.io/core:/stable:/v%s/deb/ /", version)
	if err := executor.WriteFile("/etc/apt/sources.list.d/kubernetes.list", repoLine); err != nil {
		return err
	}

	if _, err := p.exec.RunShell("apt-get update"); err != nil {
		return err
	}

	if _, err := p.exec.RunShell("apt-get install -y kubelet kubeadm kubectl"); err != nil {
		return err
	}

	if _, err := p.exec.RunShell("apt-mark hold kubelet kubeadm kubectl"); err != nil {
		return err
	}

	_, err := p.exec.Run("systemctl", "enable", "kubelet")
	return err
}

func (p *Provisioner) InitControlPlane() error {
	cfg := p.config

	// Initialize cluster
	initCmd := fmt.Sprintf("kubeadm init --apiserver-advertise-address=%s --pod-network-cidr=%s --cri-socket=unix:///var/run/crio/crio.sock --node-name=controlplane",
		cfg.Network.ControlPlaneIP, cfg.Cluster.PodCIDR)

	fmt.Println("\n>>> Initializing Kubernetes cluster...")
	if err := p.exec.RunShellWithOutput(initCmd); err != nil {
		return err
	}

	// Configure kubectl for vagrant user
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

	// Remove control-plane taint (ignore error - taint may not exist)
	fmt.Println("\n>>> Removing control-plane taint...")
	_, _ = p.exec.RunShell("kubectl taint nodes controlplane node-role.kubernetes.io/control-plane:NoSchedule- 2>/dev/null || true")

	// Install CNI
	fmt.Println("\n>>> Installing Calico CNI...")
	calicoInstaller := installer.NewCalico(cfg, p.exec)
	if err := calicoInstaller.Install(); err != nil {
		return err
	}

	// Wait for node to be ready
	fmt.Println("\n>>> Waiting for node to be ready...")
	if err := p.waitForNode("controlplane", 5*time.Minute); err != nil {
		return err
	}

	// Install MetalLB
	fmt.Println("\n>>> Installing MetalLB...")
	metallbInstaller := installer.NewMetalLB(cfg, p.exec)
	if err := metallbInstaller.Install(); err != nil {
		return err
	}

	// Install Istio
	fmt.Println("\n>>> Installing Istio...")
	istioInstaller := installer.NewIstio(cfg, p.exec)
	if err := istioInstaller.Install(); err != nil {
		return err
	}

	// Generate join command
	fmt.Println("\n>>> Generating join command...")
	if _, err := p.exec.RunShell("kubeadm token create --print-join-command > /vagrant/join-command.sh"); err != nil {
		return err
	}
	if _, err := p.exec.RunShell("chmod +x /vagrant/join-command.sh"); err != nil {
		return err
	}

	p.printSuccess()
	return nil
}

func (p *Provisioner) JoinWorker() error {
	cfg := p.config

	// Wait for join command file or API server
	fmt.Println("\n>>> Waiting for control plane...")
	if err := p.waitForAPIServer(cfg.Network.ControlPlaneIP, 5*time.Minute); err != nil {
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
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := p.exec.RunShell(fmt.Sprintf("kubectl get node %s -o jsonpath='{.status.conditions[?(@.type==\"Ready\")].status}'", name))
		if err == nil && out == "True" {
			return nil
		}
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for node %s", name)
}

func (p *Provisioner) waitForAPIServer(ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := p.exec.RunShell(fmt.Sprintf("nc -z %s 6443", ip))
		if err == nil {
			return nil
		}
		fmt.Printf("Waiting for API server at %s:6443...\n", ip)
		time.Sleep(10 * time.Second)
	}
	return fmt.Errorf("timeout waiting for API server at %s:6443", ip)
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