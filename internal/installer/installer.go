package installer

// Installer is the common contract implemented by every component installer.
// It lets the provisioner orchestrate components from a declarative table
// instead of hardcoding each call site.
type Installer interface {
	// Name returns a human-readable component name used in progress output
	// and error messages.
	Name() string
	// Install renders and applies the component's manifests, polling for
	// readiness. It returns an error if the component fails to install.
	Install() error
}

// Compile-time verification that every installer satisfies the interface.
var (
	_ Installer = (*MetalLB)(nil)
	_ Installer = (*Istio)(nil)
	_ Installer = (*CertManager)(nil)
	_ Installer = (*MetricsServer)(nil)
	_ Installer = (*VPA)(nil)
	_ Installer = (*KEDA)(nil)
	_ Installer = (*NFSProvisioner)(nil)
	_ Installer = (*VaultInstaller)(nil)
	_ Installer = (*VaultSecretsOperator)(nil)
	_ Installer = (*Monitoring)(nil)
	_ Installer = (*Loki)(nil)
	_ Installer = (*Tempo)(nil)
	_ Installer = (*Kiali)(nil)
	_ Installer = (*Keycloak)(nil)
	_ Installer = (*Ollama)(nil)
	_ Installer = (*Karpor)(nil)
	_ Installer = (*Calico)(nil)
)

func (m *MetalLB) Name() string              { return "MetalLB" }
func (i *Istio) Name() string                { return "Istio" }
func (c *CertManager) Name() string          { return "cert-manager" }
func (m *MetricsServer) Name() string        { return "Metrics Server" }
func (v *VPA) Name() string                  { return "VPA (Vertical Pod Autoscaler)" }
func (k *KEDA) Name() string                 { return "KEDA (Event-Driven Autoscaling)" }
func (n *NFSProvisioner) Name() string       { return "NFS Storage Provisioner" }
func (v *VaultInstaller) Name() string       { return "Vault (secrets management)" }
func (v *VaultSecretsOperator) Name() string { return "Vault Secrets Operator" }
func (m *Monitoring) Name() string           { return "Monitoring Stack" }
func (l *Loki) Name() string                 { return "Loki Stack" }
func (t *Tempo) Name() string                { return "Tracing Stack (Tempo + OpenTelemetry)" }
func (k *Kiali) Name() string                { return "Kiali" }
func (k *Keycloak) Name() string             { return "Keycloak (OIDC)" }
func (o *Ollama) Name() string               { return "Ollama" }
func (k *Karpor) Name() string               { return "Karpor" }
func (c *Calico) Name() string               { return "Calico CNI" }
