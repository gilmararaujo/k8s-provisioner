package provisioner

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

// writeFile writes content to path, or prints the intent and skips when in
// dry-run mode (file writes bypass the executor, so they need their own guard).
func (p *Provisioner) writeFile(path, content string) error {
	if p.dryRun {
		fmt.Printf("[dry-run] write %s\n", path)
		return nil
	}
	return executor.WriteFile(path, content)
}

func (p *Provisioner) InstallCommon() error {
	steps := []struct {
		name string
		fn   func() error
	}{
		{"Disabling swap", p.disableSwap},
		{"Loading kernel modules", p.loadKernelModules},
		{"Configuring sysctl", p.configureSysctl},
		{"Configuring DNS", p.configureDNS},
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
	if err := p.writeFile("/etc/modules-load.d/k8s.conf", modules); err != nil {
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

	if err := p.writeFile("/etc/sysctl.d/k8s.conf", sysctl); err != nil {
		return err
	}

	_, err := p.exec.RunShell("sysctl --system")
	return err
}

func (p *Provisioner) configureDNS() error {
	// VirtualBox NAT DHCP advertises the host's home router IP as DNS, which is
	// unreachable from inside the 10.0.2.x NAT network. Override with public resolvers,
	// prevent dhcpcd from overwriting on renewal, and lock the file with chattr.
	if _, err := p.exec.RunShell("chattr -i /etc/resolv.conf 2>/dev/null; rm -f /etc/resolv.conf"); err != nil {
		return err
	}
	if err := p.writeFile("/etc/resolv.conf", "nameserver 8.8.8.8\nnameserver 1.1.1.1\n"); err != nil {
		return err
	}
	if _, err := p.exec.RunShell("chattr +i /etc/resolv.conf"); err != nil {
		return err
	}
	_, err := p.exec.RunShell(`grep -q 'nohook resolv.conf' /etc/dhcpcd.conf 2>/dev/null || echo 'nohook resolv.conf' >> /etc/dhcpcd.conf`)
	return err
}

func (p *Provisioner) installDependencies() error {
	if _, err := p.exec.RunShell("apt-get update"); err != nil {
		return err
	}

	pkgs := "apt-transport-https ca-certificates curl gnupg conntrack ethtool socat"
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
	if err := p.writeFile("/etc/apt/sources.list.d/cri-o.list", repoLine); err != nil {
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
	if err := p.writeFile("/etc/apt/sources.list.d/kubernetes.list", repoLine); err != nil {
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
