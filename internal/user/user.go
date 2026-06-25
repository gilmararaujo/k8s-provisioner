package user

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	rbac "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// usernameRe is a DNS-1123 label: the same shape Kubernetes already requires for
// the CSR/RBAC object names we derive from the username, so nothing otherwise
// valid is rejected. It also forbids '/' and '.', which is what keeps the
// username from escaping the artifact directory in store paths (path traversal).
var usernameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// Timeout constants for user operations
const (
	certificateWaitTimeout  = 30 * time.Second
	certificatePollInterval = 1 * time.Second
)

// Manager orchestrates user creation/deletion by composing focused
// collaborators: certificate issuance (certIssuer), RBAC (rbacBinder),
// kubeconfig generation, and the local artifact store. It holds no domain logic
// of its own beyond sequencing and file I/O.
type Manager struct {
	clientset  kubernetes.Interface
	certs      *certIssuer
	rbac       *rbacBinder
	store      *store
	kubeconfig string
	outputDir  string
}

type UserConfig struct {
	Username    string
	Groups      []string
	Namespace   string
	ClusterRole string
	Role        string
	Expiration  int // days
}

// NewManager builds a Manager from an injected clientset. Tests pass a fake
// (k8s.io/client-go/kubernetes/fake) here; production code uses
// NewManagerFromKubeconfig.
func NewManager(clientset kubernetes.Interface, kubeconfig, outputDir string) *Manager {
	return &Manager{
		clientset:  clientset,
		certs:      newCertIssuer(clientset),
		rbac:       newRBACBinder(clientset),
		store:      newStore(outputDir),
		kubeconfig: kubeconfig,
		outputDir:  outputDir,
	}
}

// NewManagerFromKubeconfig is the composition-root constructor: it builds a real
// clientset from a kubeconfig file and wires up a Manager.
func NewManagerFromKubeconfig(kubeconfig, outputDir string) (*Manager, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return NewManager(clientset, kubeconfig, outputDir), nil
}

// validateGroups rejects Kubernetes-reserved groups that would grant escalated,
// unmanaged privileges via the issued client certificate. The "system:" prefix
// is reserved by Kubernetes; "system:masters" in particular is bound to
// cluster-admin out of the box and bypasses RBAC.
// validateUsername rejects names that are not a single DNS-1123 label. Beyond
// matching what Kubernetes will accept for the derived CSR/RBAC objects, this
// blocks '/' and '..' so a username cannot traverse out of the artifact
// directory when joined into store paths (e.g. `user delete ../../etc/x` would
// otherwise reach os.RemoveAll on an arbitrary path).
func validateUsername(username string) error {
	if !usernameRe.MatchString(username) {
		return fmt.Errorf("invalid username %q: must be a DNS-1123 label "+
			"(lowercase alphanumeric and '-', starting and ending alphanumeric, max 63 chars)", username)
	}
	return nil
}

func validateGroups(groups []string) error {
	for _, g := range groups {
		if strings.HasPrefix(g, "system:") {
			return fmt.Errorf("refusing to issue a certificate for reserved group %q "+
				"(the \"system:\" prefix is reserved by Kubernetes)", g)
		}
	}
	return nil
}

func (m *Manager) CreateUser(cfg UserConfig) error {
	fmt.Printf("Creating user '%s'...\n", cfg.Username)

	// Reject names that could escape the artifact directory or be rejected by
	// the Kubernetes API once derived into CSR/RBAC object names.
	if err := validateUsername(cfg.Username); err != nil {
		return err
	}

	// A CSR's Organization fields become Kubernetes groups, and this tool
	// auto-approves whatever it submits. Refuse reserved groups so an operator
	// cannot silently mint a cluster-admin (system:masters) client cert that
	// bypasses the RBAC bindings we otherwise manage.
	if err := validateGroups(cfg.Groups); err != nil {
		return err
	}

	userDir := m.store.UserDir(cfg.Username)
	if err := os.MkdirAll(userDir, 0750); err != nil {
		return fmt.Errorf("failed to create user directory: %w", err)
	}

	// Roll back partial state if any step below fails: local artifacts (key,
	// CSR, cert, kubeconfig) and the in-cluster CSR. RBAC bindings are the last
	// steps, so on their failure there is nothing earlier to undo beyond these.
	success := false
	defer func() {
		if !success {
			m.rollbackUser(cfg.Username)
		}
	}()

	// Steps 1–5: issue the signed client certificate.
	keyPath, certPath, err := m.issueClientCertificate(userDir, cfg)
	if err != nil {
		return err
	}

	// Step 6: Create kubeconfig
	fmt.Println("  Creating kubeconfig...")
	kubeconfigPath := filepath.Join(userDir, cfg.Username+".kubeconfig")
	if err := writeUserKubeconfig(userKubeconfig{
		SourceKubeconfig: m.kubeconfig,
		Username:         cfg.Username,
		KeyPath:          keyPath,
		CertPath:         certPath,
		OutputPath:       kubeconfigPath,
	}); err != nil {
		return err
	}
	fmt.Printf("  Kubeconfig saved: %s\n", kubeconfigPath)

	// Step 7: Create RBAC
	if err := m.applyRBAC(cfg); err != nil {
		return err
	}

	// Cleanup CSR from cluster (the signed cert is already saved locally).
	csrName := cfg.Username + "-csr"
	if err := m.certs.Delete(csrName); err != nil {
		fmt.Printf("  Warning: could not delete CSR %s from cluster: %v\n", csrName, err)
	}

	success = true
	fmt.Println("\nUser created successfully!")
	m.printUsage(cfg.Username, kubeconfigPath)

	return nil
}

// rollbackUser removes the partial artifacts of a failed CreateUser: the local
// directory and the in-cluster CSR. Best-effort; failures are logged as warnings.
func (m *Manager) rollbackUser(username string) {
	fmt.Printf("  Rolling back partial user '%s'...\n", username)
	if err := m.store.Remove(username); err != nil {
		fmt.Printf("  Warning: rollback of local files failed: %v\n", err)
	}
	if err := m.certs.Delete(username + "-csr"); err != nil {
		fmt.Printf("  Warning: rollback of in-cluster CSR failed: %v\n", err)
	}
}

// issueClientCertificate runs the key → CSR → submit → approve → wait → save flow
// (steps 1–5) and returns the on-disk paths of the private key and signed cert.
func (m *Manager) issueClientCertificate(userDir string, cfg UserConfig) (keyPath, certPath string, err error) {
	// Step 1: Generate RSA private key
	fmt.Println("  Generating RSA private key...")
	privateKey, err := m.certs.GenerateKey()
	if err != nil {
		return "", "", err
	}

	keyPath = filepath.Join(userDir, cfg.Username+".key")
	if err := os.WriteFile(keyPath, encodePrivateKeyPEM(privateKey), 0600); err != nil {
		return "", "", fmt.Errorf("failed to save private key: %w", err)
	}
	fmt.Printf("  Private key saved: %s\n", keyPath)

	// Step 2: Create CSR
	fmt.Println("  Creating Certificate Signing Request...")
	csrPEM, err := m.certs.CreateCSR(privateKey, cfg.Username, cfg.Groups)
	if err != nil {
		return "", "", err
	}

	csrPath := filepath.Join(userDir, cfg.Username+".csr")
	if err := os.WriteFile(csrPath, csrPEM, 0644); err != nil { // #nosec G306
		return "", "", fmt.Errorf("failed to save CSR: %w", err)
	}

	// Step 3: Submit CSR to Kubernetes
	fmt.Println("  Submitting CSR to Kubernetes...")
	csrName := cfg.Username + "-csr"
	if err := m.certs.Submit(csrName, csrPEM, cfg.Expiration); err != nil {
		return "", "", err
	}

	// Step 4: Approve CSR
	fmt.Println("  Approving CSR...")
	if err := m.certs.Approve(csrName); err != nil {
		return "", "", err
	}

	// Step 5: Wait and get certificate
	fmt.Println("  Waiting for certificate...")
	certPEM, err := m.certs.WaitForCertificate(csrName, certificateWaitTimeout)
	if err != nil {
		return "", "", err
	}

	certPath = filepath.Join(userDir, cfg.Username+".crt")
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil { // #nosec G306
		return "", "", fmt.Errorf("failed to save certificate: %w", err)
	}
	fmt.Printf("  Certificate saved: %s\n", certPath)

	return keyPath, certPath, nil
}

// applyRBAC creates the optional ClusterRoleBinding and/or namespaced RoleBinding
// requested in cfg (step 7). Empty fields are skipped.
func (m *Manager) applyRBAC(cfg UserConfig) error {
	if cfg.ClusterRole != "" {
		fmt.Printf("  Creating ClusterRoleBinding (%s)...\n", cfg.ClusterRole)
		if err := m.rbac.BindClusterRole(cfg.Username, cfg.Groups, cfg.ClusterRole); err != nil {
			return err
		}
	}

	if cfg.Role != "" && cfg.Namespace != "" {
		fmt.Printf("  Creating RoleBinding (%s in %s)...\n", cfg.Role, cfg.Namespace)
		if err := m.rbac.BindRole(cfg.Username, cfg.Groups, cfg.Role, cfg.Namespace); err != nil {
			return err
		}
	}

	return nil
}

func (m *Manager) DeleteUser(username string) error {
	fmt.Printf("Deleting user '%s'...\n", username)

	// Guard the destructive path: store.Remove → os.RemoveAll(filepath.Join(dir,
	// username)). A traversing name would delete an arbitrary directory.
	if err := validateUsername(username); err != nil {
		return err
	}

	var errs []error
	if err := m.rbac.DeleteForUser(username); err != nil {
		errs = append(errs, fmt.Errorf("rbac: %w", err))
	}
	if err := m.certs.Delete(username + "-csr"); err != nil {
		errs = append(errs, fmt.Errorf("csr: %w", err))
	}
	if err := m.store.Remove(username); err != nil {
		errs = append(errs, fmt.Errorf("local files: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("user %q only partially deleted (manual cleanup needed): %w",
			username, errors.Join(errs...))
	}

	fmt.Println("User deleted successfully!")
	return nil
}

func (m *Manager) ListUsers() error {
	return m.store.List()
}

func (m *Manager) CreateRole(name, namespace string, rules []rbac.PolicyRule) error {
	return m.rbac.CreateRole(name, namespace, rules)
}

func (m *Manager) printUsage(username, kubeconfigPath string) {
	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("Usage Instructions")
	fmt.Println("========================================")
	fmt.Println()
	fmt.Println("Option 1 - Use kubeconfig directly:")
	fmt.Printf("  kubectl --kubeconfig=%s get pods\n", kubeconfigPath)
	fmt.Println()
	fmt.Println("Option 2 - Export KUBECONFIG:")
	fmt.Printf("  export KUBECONFIG=%s\n", kubeconfigPath)
	fmt.Println("  kubectl get pods")
	fmt.Println()
	fmt.Println("Option 3 - Merge with existing kubeconfig:")
	fmt.Printf("  KUBECONFIG=~/.kube/config:%s kubectl config view --flatten > ~/.kube/config-merged\n", kubeconfigPath)
	fmt.Println("  mv ~/.kube/config-merged ~/.kube/config")
	fmt.Printf("  kubectl config use-context %s@k8s-lab\n", username)
	fmt.Println()
	fmt.Println("Test permissions:")
	fmt.Println("  kubectl auth can-i get pods")
	fmt.Println("  kubectl auth can-i --list")
	fmt.Println("========================================")
}
