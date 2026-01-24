package user

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	certificates "k8s.io/api/certificates/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

type Manager struct {
	clientset  *kubernetes.Clientset
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

func NewManager(kubeconfig, outputDir string) (*Manager, error) {
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return &Manager{
		clientset:  clientset,
		kubeconfig: kubeconfig,
		outputDir:  outputDir,
	}, nil
}

func (m *Manager) CreateUser(cfg UserConfig) error {
	fmt.Printf("Creating user '%s'...\n", cfg.Username)

	// Create output directory
	userDir := filepath.Join(m.outputDir, cfg.Username)
	if err := os.MkdirAll(userDir, 0750); err != nil {
		return fmt.Errorf("failed to create user directory: %w", err)
	}

	// Step 1: Generate RSA private key
	fmt.Println("  Generating RSA private key...")
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Save private key
	keyPath := filepath.Join(userDir, cfg.Username+".key")
	if err := m.savePrivateKey(privateKey, keyPath); err != nil {
		return err
	}
	fmt.Printf("  Private key saved: %s\n", keyPath)

	// Step 2: Create CSR
	fmt.Println("  Creating Certificate Signing Request...")
	csrPEM, err := m.createCSR(privateKey, cfg.Username, cfg.Groups)
	if err != nil {
		return err
	}

	// Save CSR
	csrPath := filepath.Join(userDir, cfg.Username+".csr")
	if err := os.WriteFile(csrPath, csrPEM, 0644); err != nil {
		return fmt.Errorf("failed to save CSR: %w", err)
	}

	// Step 3: Submit CSR to Kubernetes
	fmt.Println("  Submitting CSR to Kubernetes...")
	csrName := cfg.Username + "-csr"
	if err := m.submitCSR(csrName, csrPEM, cfg.Expiration); err != nil {
		return err
	}

	// Step 4: Approve CSR
	fmt.Println("  Approving CSR...")
	if err := m.approveCSR(csrName); err != nil {
		return err
	}

	// Step 5: Wait and get certificate
	fmt.Println("  Waiting for certificate...")
	certPEM, err := m.waitForCertificate(csrName, 30*time.Second)
	if err != nil {
		return err
	}

	// Save certificate
	certPath := filepath.Join(userDir, cfg.Username+".crt")
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to save certificate: %w", err)
	}
	fmt.Printf("  Certificate saved: %s\n", certPath)

	// Step 6: Create kubeconfig
	fmt.Println("  Creating kubeconfig...")
	kubeconfigPath := filepath.Join(userDir, cfg.Username+".kubeconfig")
	if err := m.createKubeconfig(cfg.Username, keyPath, certPath, kubeconfigPath); err != nil {
		return err
	}
	fmt.Printf("  Kubeconfig saved: %s\n", kubeconfigPath)

	// Step 7: Create RBAC
	if cfg.ClusterRole != "" {
		fmt.Printf("  Creating ClusterRoleBinding (%s)...\n", cfg.ClusterRole)
		if err := m.createClusterRoleBinding(cfg.Username, cfg.Groups, cfg.ClusterRole); err != nil {
			return err
		}
	}

	if cfg.Role != "" && cfg.Namespace != "" {
		fmt.Printf("  Creating RoleBinding (%s in %s)...\n", cfg.Role, cfg.Namespace)
		if err := m.createRoleBinding(cfg.Username, cfg.Groups, cfg.Role, cfg.Namespace); err != nil {
			return err
		}
	}

	// Cleanup CSR from cluster
	_ = m.clientset.CertificatesV1().CertificateSigningRequests().Delete(
		context.TODO(), csrName, metav1.DeleteOptions{})

	fmt.Println("\nUser created successfully!")
	m.printUsage(cfg.Username, kubeconfigPath)

	return nil
}

func (m *Manager) savePrivateKey(key *rsa.PrivateKey, path string) error {
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	if err := os.WriteFile(path, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to save private key: %w", err)
	}
	return nil
}

func (m *Manager) createCSR(key *rsa.PrivateKey, username string, groups []string) ([]byte, error) {
	subject := pkix.Name{
		CommonName: username,
	}

	if len(groups) > 0 {
		subject.Organization = groups
	}

	template := &x509.CertificateRequest{
		Subject:            subject,
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, fmt.Errorf("failed to create CSR: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	return csrPEM, nil
}

func (m *Manager) submitCSR(name string, csrPEM []byte, expirationDays int) error {
	// Delete existing CSR if exists
	_ = m.clientset.CertificatesV1().CertificateSigningRequests().Delete(
		context.TODO(), name, metav1.DeleteOptions{})

	expirationSeconds := expirationDays * 24 * 60 * 60 // days to seconds
	expiration := int32(expirationSeconds)             // #nosec G115

	csr := &certificates.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: certificates.CertificateSigningRequestSpec{
			Request:           csrPEM,
			SignerName:        "kubernetes.io/kube-apiserver-client",
			ExpirationSeconds: &expiration,
			Usages: []certificates.KeyUsage{
				certificates.UsageClientAuth,
			},
		},
	}

	_, err := m.clientset.CertificatesV1().CertificateSigningRequests().Create(
		context.TODO(), csr, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to submit CSR: %w", err)
	}

	return nil
}

func (m *Manager) approveCSR(name string) error {
	csr, err := m.clientset.CertificatesV1().CertificateSigningRequests().Get(
		context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get CSR: %w", err)
	}

	csr.Status.Conditions = append(csr.Status.Conditions, certificates.CertificateSigningRequestCondition{
		Type:           certificates.CertificateApproved,
		Status:         "True",
		Reason:         "ApprovedByK8sProvisioner",
		Message:        "Approved by k8s-provisioner user command",
		LastUpdateTime: metav1.Now(),
	})

	_, err = m.clientset.CertificatesV1().CertificateSigningRequests().UpdateApproval(
		context.TODO(), name, csr, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to approve CSR: %w", err)
	}

	return nil
}

func (m *Manager) waitForCertificate(name string, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		csr, err := m.clientset.CertificatesV1().CertificateSigningRequests().Get(
			context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to get CSR: %w", err)
		}

		if len(csr.Status.Certificate) > 0 {
			return csr.Status.Certificate, nil
		}

		time.Sleep(1 * time.Second)
	}

	return nil, fmt.Errorf("timeout waiting for certificate")
}

func (m *Manager) createKubeconfig(username, keyPath, certPath, outputPath string) error {
	// Load existing kubeconfig to get cluster info
	config, err := clientcmd.LoadFromFile(m.kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Get current context's cluster
	currentContext := config.CurrentContext
	if currentContext == "" {
		return fmt.Errorf("no current context in kubeconfig")
	}

	contextConfig := config.Contexts[currentContext]
	if contextConfig == nil {
		return fmt.Errorf("context not found: %s", currentContext)
	}

	clusterConfig := config.Clusters[contextConfig.Cluster]
	if clusterConfig == nil {
		return fmt.Errorf("cluster not found: %s", contextConfig.Cluster)
	}

	// Read key and cert
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read key: %w", err)
	}

	certData, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read cert: %w", err)
	}

	// Create new kubeconfig
	clusterName := contextConfig.Cluster
	contextName := fmt.Sprintf("%s@%s", username, clusterName)

	newConfig := api.NewConfig()

	// Add cluster
	newConfig.Clusters[clusterName] = &api.Cluster{
		Server:                   clusterConfig.Server,
		CertificateAuthorityData: clusterConfig.CertificateAuthorityData,
	}

	// Add user credentials
	newConfig.AuthInfos[username] = &api.AuthInfo{
		ClientCertificateData: certData,
		ClientKeyData:         keyData,
	}

	// Add context
	newConfig.Contexts[contextName] = &api.Context{
		Cluster:  clusterName,
		AuthInfo: username,
	}

	newConfig.CurrentContext = contextName

	// Write kubeconfig
	if err := clientcmd.WriteToFile(*newConfig, outputPath); err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	return nil
}

func (m *Manager) createClusterRoleBinding(username string, groups []string, clusterRole string) error {
	binding := &rbac.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s-binding", username, clusterRole),
		},
		Subjects: []rbac.Subject{
			{
				Kind:     "User",
				Name:     username,
				APIGroup: "rbac.authorization.k8s.io",
			},
		},
		RoleRef: rbac.RoleRef{
			Kind:     "ClusterRole",
			Name:     clusterRole,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	// Add groups as subjects
	for _, group := range groups {
		binding.Subjects = append(binding.Subjects, rbac.Subject{
			Kind:     "Group",
			Name:     group,
			APIGroup: "rbac.authorization.k8s.io",
		})
	}

	_, err := m.clientset.RbacV1().ClusterRoleBindings().Create(
		context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fmt.Printf("  ClusterRoleBinding already exists, skipping...\n")
			return nil
		}
		return fmt.Errorf("failed to create ClusterRoleBinding: %w", err)
	}

	return nil
}

func (m *Manager) createRoleBinding(username string, groups []string, role, namespace string) error {
	binding := &rbac.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-binding", username, role),
			Namespace: namespace,
		},
		Subjects: []rbac.Subject{
			{
				Kind:     "User",
				Name:     username,
				APIGroup: "rbac.authorization.k8s.io",
			},
		},
		RoleRef: rbac.RoleRef{
			Kind:     "Role",
			Name:     role,
			APIGroup: "rbac.authorization.k8s.io",
		},
	}

	// Add groups as subjects
	for _, group := range groups {
		binding.Subjects = append(binding.Subjects, rbac.Subject{
			Kind:     "Group",
			Name:     group,
			APIGroup: "rbac.authorization.k8s.io",
		})
	}

	_, err := m.clientset.RbacV1().RoleBindings(namespace).Create(
		context.TODO(), binding, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fmt.Printf("  RoleBinding already exists, skipping...\n")
			return nil
		}
		return fmt.Errorf("failed to create RoleBinding: %w", err)
	}

	return nil
}

func (m *Manager) DeleteUser(username string) error {
	fmt.Printf("Deleting user '%s'...\n", username)

	// Delete ClusterRoleBindings
	bindings, err := m.clientset.RbacV1().ClusterRoleBindings().List(
		context.TODO(), metav1.ListOptions{})
	if err == nil {
		for _, b := range bindings.Items {
			if strings.HasPrefix(b.Name, username+"-") {
				fmt.Printf("  Deleting ClusterRoleBinding: %s\n", b.Name)
				_ = m.clientset.RbacV1().ClusterRoleBindings().Delete(
					context.TODO(), b.Name, metav1.DeleteOptions{})
			}
		}
	}

	// Delete RoleBindings in all namespaces
	namespaces, err := m.clientset.CoreV1().Namespaces().List(
		context.TODO(), metav1.ListOptions{})
	if err == nil {
		for _, ns := range namespaces.Items {
			roleBindings, _ := m.clientset.RbacV1().RoleBindings(ns.Name).List(
				context.TODO(), metav1.ListOptions{})
			for _, rb := range roleBindings.Items {
				if strings.HasPrefix(rb.Name, username+"-") {
					fmt.Printf("  Deleting RoleBinding: %s/%s\n", ns.Name, rb.Name)
					_ = m.clientset.RbacV1().RoleBindings(ns.Name).Delete(
						context.TODO(), rb.Name, metav1.DeleteOptions{})
				}
			}
		}
	}

	// Delete CSR if exists
	csrName := username + "-csr"
	_ = m.clientset.CertificatesV1().CertificateSigningRequests().Delete(
		context.TODO(), csrName, metav1.DeleteOptions{})

	// Delete local files
	userDir := filepath.Join(m.outputDir, username)
	if _, err := os.Stat(userDir); err == nil {
		fmt.Printf("  Deleting local files: %s\n", userDir)
		_ = os.RemoveAll(userDir)
	}

	fmt.Println("User deleted successfully!")
	return nil
}

func (m *Manager) ListUsers() error {
	fmt.Println("Users with certificate-based authentication:")
	fmt.Println()

	// List from output directory
	entries, err := os.ReadDir(m.outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No users found.")
			return nil
		}
		return err
	}

	if len(entries) == 0 {
		fmt.Println("No users found.")
		return nil
	}

	fmt.Printf("%-20s %-40s %-20s\n", "USERNAME", "KUBECONFIG", "GROUPS")
	fmt.Printf("%-20s %-40s %-20s\n", strings.Repeat("-", 20), strings.Repeat("-", 40), strings.Repeat("-", 20))

	for _, entry := range entries {
		if entry.IsDir() {
			username := entry.Name()
			kubeconfigPath := filepath.Join(m.outputDir, username, username+".kubeconfig")
			certPath := filepath.Join(m.outputDir, username, username+".crt")

			if _, err := os.Stat(kubeconfigPath); err == nil {
				groups := m.getGroupsFromCert(certPath)
				fmt.Printf("%-20s %-40s %-20s\n", username, kubeconfigPath, groups)
			}
		}
	}

	return nil
}

func (m *Manager) getGroupsFromCert(certPath string) string {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return "-"
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "-"
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "-"
	}

	if len(cert.Subject.Organization) > 0 {
		return strings.Join(cert.Subject.Organization, ",")
	}

	return "-"
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

func (m *Manager) CreateRole(name, namespace string, rules []rbac.PolicyRule) error {
	role := &rbac.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: rules,
	}

	_, err := m.clientset.RbacV1().Roles(namespace).Create(
		context.TODO(), role, metav1.CreateOptions{})
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			fmt.Printf("Role '%s' already exists in namespace '%s'\n", name, namespace)
			return nil
		}
		return fmt.Errorf("failed to create Role: %w", err)
	}

	fmt.Printf("Role '%s' created in namespace '%s'\n", name, namespace)
	return nil
}

// GetDefaultDeveloperRules returns common rules for developers
func GetDefaultDeveloperRules() []rbac.PolicyRule {
	return []rbac.PolicyRule{
		{
			APIGroups: []string{"", "apps", "extensions", "batch"},
			Resources: []string{
				"pods", "pods/log", "pods/exec",
				"deployments", "replicasets", "statefulsets", "daemonsets",
				"services", "endpoints",
				"configmaps", "secrets",
				"jobs", "cronjobs",
				"persistentvolumeclaims",
			},
			Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"networking.k8s.io"},
			Resources: []string{"ingresses", "networkpolicies"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"autoscaling"},
			Resources: []string{"horizontalpodautoscalers"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
	}
}
