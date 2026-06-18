package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/config"
	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

type Keycloak struct {
	config *config.Config
	exec   executor.ShellExecutor
}

type keycloakCreds struct {
	adminUsername     string
	adminPassword     string
	postgresUsername  string
	postgresPassword  string
	grafanaSecret     string
	k8sAdminPassword  string
	developerPassword string
}

func NewKeycloak(cfg *config.Config, exec executor.ShellExecutor) *Keycloak {
	return &Keycloak{config: cfg, exec: exec}
}

func (k *Keycloak) Install() error {
	fmt.Println("Installing Keycloak (OIDC Identity Provider)...")

	cpIP := k.config.Network.ControlPlaneIP
	issuerURL := "https://keycloak.local/realms/k8s"

	creds, err := k.resolveCredentials()
	if err != nil {
		return err
	}

	// Wait for VSO to sync the freshly-written Vault credentials into the keycloak-admin K8s
	// secret before creating the pod — env vars are captured at container start time and
	// Keycloak 26.x will not create the admin account if KEYCLOAK_ADMIN is empty.
	fmt.Println("Waiting for VSO to sync keycloak-admin secret...")
	if err := k.waitForAdminSecret(adminSecretSyncTimeout); err != nil {
		fmt.Printf("Warning: keycloak-admin secret may not be ready: %v\n", err)
	}

	fmt.Println("Deploying Keycloak...")
	if err := k.deployKeycloak(creds); err != nil {
		return err
	}

	fmt.Println("Waiting for Keycloak to be ready (first start includes build step, ~5-8 min)...")
	if err := k.waitForReady(keycloakStartTimeout); err != nil {
		return fmt.Errorf("keycloak did not become ready: %w", err)
	}

	fmt.Println("Configuring realm, clients, and users...")
	if err := k.configureRealm(cpIP, creds); err != nil {
		return fmt.Errorf("realm configuration failed: %w", err)
	}

	fmt.Println("Patching API server with OIDC authentication...")
	if err := k.patchAPIServer(issuerURL); err != nil {
		fmt.Printf("Warning: API server patch failed: %v\n", err)
	}

	if k.config.Components.ServiceMesh == "istio" {
		fmt.Println("Creating Istio Gateway for Keycloak...")
		if err := k.createGateway(); err != nil {
			fmt.Printf("Warning: Failed to create Keycloak gateway: %v\n", err)
		}
	}

	fmt.Println("Storing kubeconfig-oidc in Vault for distribution...")
	if err := k.storeKubeconfigInVault(cpIP, issuerURL); err != nil {
		fmt.Printf("Warning: could not store kubeconfig in Vault: %v\n", err)
	}

	fmt.Println("Keycloak installed successfully!")
	k.printAccessInfo(cpIP, issuerURL)
	return nil
}

// ConfigureGrafanaOAuth applies Grafana OAuth2 configuration after Keycloak is fully installed.
// Called from the provisioner as a separate step so it runs even if Install() had partial failures.
func (k *Keycloak) ConfigureGrafanaOAuth() error {
	if k.config.Components.Monitoring != "prometheus-stack" {
		return nil
	}
	cpIP := k.config.Network.ControlPlaneIP
	creds, err := k.resolveCredentials()
	if err != nil {
		return err
	}

	fmt.Println("Configuring Grafana OAuth2 with Keycloak...")
	var oauthErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if oauthErr = k.configureGrafanaOAuth(cpIP, creds); oauthErr == nil {
			break
		}
		fmt.Printf("Attempt %d/3 failed: %v — retrying in 20s...\n", attempt, oauthErr)
		time.Sleep(oauthRetryDelay)
	}
	return oauthErr
}

func (k *Keycloak) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Wait for PostgreSQL StatefulSet rollout
	for time.Now().Before(deadline) {
		out, _ := k.exec.RunShell("kubectl rollout status statefulset/postgres -n keycloak --timeout=10s 2>&1")
		if strings.Contains(out, "rolling update complete") || strings.Contains(out, "roll out complete") {
			fmt.Println("PostgreSQL is running!")
			break
		}
		fmt.Println("Waiting for PostgreSQL...")
		time.Sleep(defaultPollInterval)
	}

	// Wait for Keycloak Deployment rollout — reliable for pods with Istio sidecars
	// since rollout status requires ALL containers (including sidecar) to be ready.
	for time.Now().Before(deadline) {
		out, _ := k.exec.RunShell("kubectl rollout status deployment/keycloak -n keycloak --timeout=10s 2>&1")
		if strings.Contains(out, "successfully rolled out") {
			fmt.Println("Keycloak is ready!")
			return nil
		}
		fmt.Println("Waiting for Keycloak to be healthy (first start includes build step)...")
		time.Sleep(defaultPollInterval)
	}

	return fmt.Errorf("timeout waiting for Keycloak to be ready")
}

// waitForAdminSecret blocks until the keycloak-admin K8s secret (managed by VSO) has a
// non-empty username field. This prevents a race where the pod is created before VSO has
// synced the Vault credentials, causing Keycloak to start without an admin account.
func (k *Keycloak) waitForAdminSecret(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := k.exec.RunShell(`kubectl get secret keycloak-admin -n keycloak -o jsonpath='{.data.username}' 2>/dev/null | base64 -d 2>/dev/null`)
		if strings.TrimSpace(out) != "" {
			fmt.Println("keycloak-admin secret synced!")
			return nil
		}
		fmt.Println("Waiting for keycloak-admin secret to be populated by VSO...")
		time.Sleep(shortPollInterval)
	}
	return fmt.Errorf("timeout: keycloak-admin secret not populated within %s", timeout)
}

func (k *Keycloak) waitForSecret(namespace, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := k.exec.RunShell(fmt.Sprintf(
			"kubectl get secret %s -n %s 2>/dev/null", name, namespace))
		if err == nil {
			return nil
		}
		fmt.Printf("Waiting for secret %s/%s...\n", namespace, name)
		time.Sleep(defaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for secret %s/%s", namespace, name)
}

func (k *Keycloak) printAccessInfo(cpIP, issuerURL string) {
	fmt.Println("\n========================================")
	fmt.Println("Keycloak Access Information")
	fmt.Println("========================================")
	fmt.Printf("\nAdmin Console: http://%s:30080\n", cpIP)
	fmt.Println("  (or http://keycloak.local via Istio Gateway)")
	fmt.Println("\nAdmin credentials (stored in Vault):")
	fmt.Println("  vault kv get -field=keycloak_admin_username secret/k8s-provisioner/api-keys")
	fmt.Println("  vault kv get -field=keycloak_admin_password secret/k8s-provisioner/api-keys")
	fmt.Println("\nTest users (realm: k8s) — senhas no Vault:")
	fmt.Println("  k8sadmin  (group: k8s-admins  → cluster-admin)")
	fmt.Println("    vault kv get -field=keycloak_k8sadmin_password secret/k8s-provisioner/api-keys")
	fmt.Println("  developer (group: k8s-developers → view)")
	fmt.Println("    vault kv get -field=keycloak_developer_password secret/k8s-provisioner/api-keys")
	fmt.Println("\n--- kubectl OIDC login (kubelogin) ---")
	fmt.Println("Install kubelogin:")
	fmt.Println("  brew install int128/kubelogin/kubelogin   # Mac")
	fmt.Println("  kubectl krew install oidc-login           # via krew")
	fmt.Println("\nAdd OIDC credentials to kubeconfig:")
	fmt.Printf(`  kubectl config set-credentials oidc \
    --exec-api-version=client.authentication.k8s.io/v1beta1 \
    --exec-command=kubectl \
    --exec-arg=oidc-login \
    --exec-arg=get-token \
    --exec-arg=--oidc-issuer-url=%s \
    --exec-arg=--oidc-client-id=kubectl \
    --exec-arg=--oidc-pkce-method=auto \
    --exec-arg=--insecure-skip-tls-verify \
    --exec-arg=--listen-address=%s
`, issuerURL, kubeloginListenAddr)
	fmt.Println("\nTest login:")
	fmt.Println("  kubectl get nodes --user=oidc")
	fmt.Println("\n--- Grafana SSO ---")
	fmt.Println("  Grafana now uses Keycloak for login.")
	fmt.Println("  Local admin login still works (user 'admin'; password in Vault or shown during install).")
	fmt.Println("========================================")
}
