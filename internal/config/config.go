package config

import (
	"fmt"
	"net"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Cluster    ClusterConfig    `yaml:"cluster"`
	Versions   VersionsConfig   `yaml:"versions"`
	Network    NetworkConfig    `yaml:"network"`
	Storage    StorageConfig    `yaml:"storage"`
	Nodes      []NodeConfig     `yaml:"nodes"`
	Components ComponentsConfig `yaml:"components"`
	KarporAI     KarporAIConfig     `yaml:"karpor_ai"`
	Ollama       OllamaConfig       `yaml:"ollama"`
	Vault        VaultConfig        `yaml:"vault"`
	Provisioning ProvisioningConfig `yaml:"provisioning"`
}

type VaultConfig struct {
	Enabled bool   `yaml:"enabled"` // toggles Vault; addr is derived when empty
	Addr    string `yaml:"addr"`    // optional override; empty = derive from storage node
	Token   string `yaml:"token"`
}

// ProvisioningConfig controls node-to-node SSH used to transport Vault init data
// from the controlplane to the storage node. Defaults target the Vagrant lab box;
// override ssh_key_path (preferred) or ssh_password for non-lab environments
// instead of relying on the hard-coded Vagrant credentials.
type ProvisioningConfig struct {
	SSHUser     string `yaml:"ssh_user"`     // default: vagrant
	SSHPassword string `yaml:"ssh_password"` // password auth; ignored when ssh_key_path is set
	SSHKeyPath  string `yaml:"ssh_key_path"` // private key for key-based auth (preferred)
}

// SSHUser returns the configured SSH user, defaulting to the Vagrant box user.
func (c *Config) SSHUser() string {
	if c.Provisioning.SSHUser != "" {
		return c.Provisioning.SSHUser
	}
	return "vagrant"
}

type OllamaConfig struct {
	APIKey string `yaml:"api_key"` // Ollama cloud API key (from https://ollama.com/settings/keys)
}

type ClusterConfig struct {
	Name        string `yaml:"name"`
	PodCIDR     string `yaml:"pod_cidr"`
	ServiceCIDR string `yaml:"service_cidr"`
}

type VersionsConfig struct {
	Kubernetes         string `yaml:"kubernetes"`
	CriO               string `yaml:"crio"`
	Calico             string `yaml:"calico"`
	MetalLB            string `yaml:"metallb"`
	Istio              string `yaml:"istio"`
	Karpor             string `yaml:"karpor"`
	Grafana            string `yaml:"grafana"`
	Loki               string `yaml:"loki"`
	Alloy              string `yaml:"alloy"`
	Tempo              string `yaml:"tempo"`
	OtelCollector      string `yaml:"otel_collector"`
	Keycloak           string `yaml:"keycloak"`
	Postgres           string `yaml:"postgres"`
	Kiali              string `yaml:"kiali"`
	NodeExporter       string `yaml:"node_exporter"`
	KubeStateMetrics   string `yaml:"kube_state_metrics"`
	MetricsServer      string `yaml:"metrics_server"`
	PrometheusOperator string `yaml:"prometheus_operator"`
	CertManager        string `yaml:"cert_manager"`
}

type NetworkConfig struct {
	Interface      string `yaml:"interface"`
	ControlPlaneIP string `yaml:"controlplane_ip"`
	MetalLBRange   string `yaml:"metallb_range"`
}

type StorageConfig struct {
	NFSServer      string `yaml:"nfs_server"`
	NFSPath        string `yaml:"nfs_path"`
	DefaultDynamic bool   `yaml:"default_dynamic"` // If true, nfs-dynamic is the default StorageClass
}

type NodeConfig struct {
	Name string `yaml:"name"`
	IP   string `yaml:"ip"`
	Role string `yaml:"role"`
}

type ComponentsConfig struct {
	CNI          string `yaml:"cni"`
	LoadBalancer string `yaml:"load_balancer"`
	ServiceMesh  string `yaml:"service_mesh"`
	Monitoring   string `yaml:"monitoring"`
	Logging      string `yaml:"logging"`
	Tracing      string `yaml:"tracing"` // Options: otel-tempo, none
	Karpor       string `yaml:"karpor"`
	Keycloak     string `yaml:"keycloak"`
	VPA          string `yaml:"vpa"`  // Options: enabled, disabled
	KEDA         string `yaml:"keda"` // Options: enabled, disabled
}

type KarporAIConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Backend   string `yaml:"backend"`
	AuthToken string `yaml:"auth_token"`
	BaseURL   string `yaml:"base_url"`
	Model     string `yaml:"model"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Secrets may be supplied via environment so real values never need to live in
	// the tracked config.yaml (which carries only empty placeholders). Env wins.
	applyEnvSecrets(&cfg)

	// Node IPs in `nodes:` are the single source of truth. Derive the control
	// plane IP from the controlplane node when network.controlplane_ip is unset,
	// so the address is not duplicated in config.yaml.
	if cfg.Network.ControlPlaneIP == "" {
		if cp := cfg.GetControlPlane(); cp != nil {
			cfg.Network.ControlPlaneIP = cp.IP
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &cfg, nil
}

// applyEnvSecrets overrides secret-bearing fields from the environment so real
// secrets never have to be written into the tracked config.yaml. An empty env
// var leaves the configured (or placeholder) value untouched.
func applyEnvSecrets(cfg *Config) {
	if v := os.Getenv("K8S_PROV_VAULT_TOKEN"); v != "" {
		cfg.Vault.Token = v
	}
	if v := os.Getenv("K8S_PROV_SSH_PASSWORD"); v != "" {
		cfg.Provisioning.SSHPassword = v
	}
	if v := os.Getenv("OLLAMA_API_KEY"); v != "" {
		cfg.Ollama.APIKey = v
	}
	if v := os.Getenv("KARPOR_AUTH_TOKEN"); v != "" {
		cfg.KarporAI.AuthToken = v
	}
}

// Validate checks all required fields and formats
func (c *Config) Validate() error {
	var errors []string

	// Cluster validation
	if c.Cluster.Name == "" {
		errors = append(errors, "cluster.name is required")
	}
	if c.Cluster.PodCIDR == "" {
		errors = append(errors, "cluster.pod_cidr is required")
	} else if !isValidCIDR(c.Cluster.PodCIDR) {
		errors = append(errors, fmt.Sprintf("cluster.pod_cidr '%s' is not a valid CIDR", c.Cluster.PodCIDR))
	}
	if c.Cluster.ServiceCIDR == "" {
		errors = append(errors, "cluster.service_cidr is required")
	} else if !isValidCIDR(c.Cluster.ServiceCIDR) {
		errors = append(errors, fmt.Sprintf("cluster.service_cidr '%s' is not a valid CIDR", c.Cluster.ServiceCIDR))
	}

	// Versions validation
	if c.Versions.Kubernetes == "" {
		errors = append(errors, "versions.kubernetes is required")
	}
	if c.Versions.CriO == "" {
		errors = append(errors, "versions.crio is required")
	}

	// Network validation
	if c.Network.Interface == "" {
		errors = append(errors, "network.interface is required")
	}
	if c.Network.ControlPlaneIP == "" {
		errors = append(errors, "network.controlplane_ip is required")
	} else if !isValidIP(c.Network.ControlPlaneIP) {
		errors = append(errors, fmt.Sprintf("network.controlplane_ip '%s' is not a valid IP address", c.Network.ControlPlaneIP))
	}
	if c.Network.MetalLBRange != "" {
		if err := validateIPRange(c.Network.MetalLBRange); err != nil {
			errors = append(errors, fmt.Sprintf("network.metallb_range: %v", err))
		}
	}

	// Storage validation
	if c.Storage.NFSPath == "" {
		errors = append(errors, "storage.nfs_path is required")
	}

	// Vault validation: when enabled, an address must be resolvable (explicit
	// vault.addr, or a storage node with an ip to derive it from).
	if c.Vault.Enabled && c.VaultAddress() == "" {
		errors = append(errors, "vault.enabled is true but no address could be resolved (set vault.addr or define a storage node with an ip)")
	}

	// Provisioning validation
	if err := validateSSHPassword(c.Provisioning.SSHPassword); err != nil {
		errors = append(errors, fmt.Sprintf("provisioning.ssh_password: %v", err))
	}

	// Nodes validation
	errors = append(errors, validateNodes(c.Nodes)...)

	if !hasControlPlaneNode(c.Nodes) {
		errors = append(errors, "at least one node with role 'controlplane' is required")
	}

	// Component toggles are matched by string equality at use sites, so a typo
	// (e.g. "prometheus_stack") silently skips the component instead of erroring.
	// Validate against the documented option sets. Empty = explicitly unset/skip.
	enumChecks := []struct {
		name    string
		value   string
		allowed []string
	}{
		{"components.service_mesh", c.Components.ServiceMesh, []string{"istio", "none"}},
		{"components.monitoring", c.Components.Monitoring, []string{"prometheus-stack", "none"}},
		{"components.logging", c.Components.Logging, []string{"loki", "none"}},
		{"components.tracing", c.Components.Tracing, []string{"otel-tempo", "none"}},
		{"components.karpor", c.Components.Karpor, []string{"enabled", "disabled", "none"}},
		{"components.keycloak", c.Components.Keycloak, []string{"enabled", "disabled", "none"}},
		{"components.vpa", c.Components.VPA, []string{"enabled", "disabled", "none"}},
		{"components.keda", c.Components.KEDA, []string{"enabled", "disabled", "none"}},
	}
	for _, e := range enumChecks {
		if e.value == "" {
			continue
		}
		if !slices.Contains(e.allowed, e.value) {
			errors = append(errors, fmt.Sprintf("%s '%s' is invalid (allowed: %s)",
				e.name, e.value, strings.Join(e.allowed, ", ")))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("%s", strings.Join(errors, "; "))
	}

	return nil
}

// validateNodes checks each node's name, role and (when set) IP format.
func validateNodes(nodes []NodeConfig) []string {
	var errs []string
	if len(nodes) == 0 {
		errs = append(errs, "at least one node must be defined")
	}
	validRoles := map[string]bool{"storage": true, "controlplane": true, "worker": true}
	for i, node := range nodes {
		if node.Name == "" {
			errs = append(errs, fmt.Sprintf("nodes[%d].name is required", i))
		}
		if node.Role == "" {
			errs = append(errs, fmt.Sprintf("nodes[%d].role is required", i))
		} else if !validRoles[node.Role] {
			errs = append(errs, fmt.Sprintf("nodes[%d].role '%s' is invalid (must be: storage, controlplane, or worker)", i, node.Role))
		}
		if node.IP != "" && !isValidIP(node.IP) {
			errs = append(errs, fmt.Sprintf("nodes[%d].ip '%s' is not a valid IP address", i, node.IP))
		}
	}
	return errs
}

func hasControlPlaneNode(nodes []NodeConfig) bool {
	for _, node := range nodes {
		if node.Role == "controlplane" {
			return true
		}
	}
	return false
}

// validateSSHPassword rejects shell-dangerous characters in the SSH password.
// The password is interpolated into `sshpass -p '<pw>'` and handed to `sh -c`
// (internal/installer/vault.go), so a single quote breaks out of the quoting and
// allows command injection; the other characters are rejected as defense in depth
// and because they also defeat the credential scrubbing applied to log output.
// Empty is allowed (key-based auth, or the Vagrant default, is used instead).
func validateSSHPassword(pw string) error {
	if strings.ContainsAny(pw, "'`$;\n\r") {
		return fmt.Errorf("must not contain any of ' ` $ ; or newlines " +
			"(use ssh_key_path for credentials with special characters)")
	}
	return nil
}

// isValidIP checks if the string is a valid IPv4 or IPv6 address
func isValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

// isValidCIDR checks if the string is a valid CIDR notation
func isValidCIDR(cidr string) bool {
	_, _, err := net.ParseCIDR(cidr)
	return err == nil
}

// validateIPRange validates a MetalLB IP range format (e.g., "192.168.56.200-192.168.56.250")
func validateIPRange(ipRange string) error {
	parts := strings.Split(ipRange, "-")
	if len(parts) != 2 {
		return fmt.Errorf("'%s' must be in format 'startIP-endIP'", ipRange)
	}

	startIP := net.ParseIP(strings.TrimSpace(parts[0]))
	endIP := net.ParseIP(strings.TrimSpace(parts[1]))

	if startIP == nil {
		return fmt.Errorf("start IP '%s' is invalid", parts[0])
	}
	if endIP == nil {
		return fmt.Errorf("end IP '%s' is invalid", parts[1])
	}

	// Check that both are the same IP version
	startV4 := startIP.To4()
	endV4 := endIP.To4()
	if (startV4 == nil) != (endV4 == nil) {
		return fmt.Errorf("start and end IPs must be the same version (IPv4 or IPv6)")
	}

	return nil
}

func (c *Config) GetControlPlane() *NodeConfig {
	for _, node := range c.Nodes {
		if node.Role == "controlplane" {
			return &node
		}
	}
	return nil
}

func (c *Config) GetStorageNode() *NodeConfig {
	for _, node := range c.Nodes {
		if node.Role == "storage" {
			return &node
		}
	}
	return nil
}

// StorageIP returns the storage node IP from config (nodes:), or "" if no
// storage node is defined. No hardcoded fallback — node IPs are authoritative.
func (c *Config) StorageIP() string {
	if n := c.GetStorageNode(); n != nil {
		return n.IP
	}
	return ""
}

// VaultAddress returns the configured Vault address. vault.addr (which also acts
// as the Vault enable switch, see VaultConfig.Enabled) takes precedence; when
// unset it is derived from the storage node IP so no address is hardcoded in Go.
func (c *Config) VaultAddress() string {
	if c.Vault.Addr != "" {
		return c.Vault.Addr
	}
	if ip := c.StorageIP(); ip != "" {
		return fmt.Sprintf("http://%s:8200", ip)
	}
	return ""
}

func (c *Config) GetWorkers() []NodeConfig {
	var workers []NodeConfig
	for _, node := range c.Nodes {
		if node.Role == "worker" {
			workers = append(workers, node)
		}
	}
	return workers
}
