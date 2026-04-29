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
	exec   executor.CommandExecutor
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

func NewKeycloak(cfg *config.Config, exec executor.CommandExecutor) *Keycloak {
	return &Keycloak{config: cfg, exec: exec}
}

func (k *Keycloak) resolveCredentials() keycloakCreds {
	creds := keycloakCreds{
		adminUsername:     "admin",
		adminPassword:     "Admin@Keycloak123",
		postgresUsername:  "keycloak",
		postgresPassword:  "Keycloak@Pg123",
		grafanaSecret:     "grafana-oidc-secret",
		k8sAdminPassword:  "Admin@K8s123",
		developerPassword: "Dev@K8s123",
	}

	token := ResolveVaultToken(k.config.Vault.Token)
	if k.config.Vault.Addr == "" || token == "" {
		fmt.Println("Warning: Vault not configured, using default Keycloak credentials")
		return creds
	}

	vault := NewVaultClient(k.config.Vault.Addr, token)
	const vaultPath = "k8s-provisioner/api-keys"

	existing, err := vault.ReadSecret(vaultPath)
	if err != nil {
		fmt.Printf("Warning: could not read Vault secrets: %v — using defaults\n", err)
		return creds
	}

	// Write defaults for missing keys, then read back
	updates := map[string]string{}
	if existing == nil || existing["keycloak_admin_username"] == "" {
		updates["keycloak_admin_username"] = creds.adminUsername
	} else {
		creds.adminUsername = existing["keycloak_admin_username"]
	}
	if existing == nil || existing["keycloak_admin_password"] == "" {
		updates["keycloak_admin_password"] = creds.adminPassword
	} else {
		creds.adminPassword = existing["keycloak_admin_password"]
	}
	if existing == nil || existing["keycloak_postgres_username"] == "" {
		updates["keycloak_postgres_username"] = creds.postgresUsername
	} else {
		creds.postgresUsername = existing["keycloak_postgres_username"]
	}
	if existing == nil || existing["keycloak_postgres_password"] == "" {
		updates["keycloak_postgres_password"] = creds.postgresPassword
	} else {
		creds.postgresPassword = existing["keycloak_postgres_password"]
	}
	if existing == nil || existing["keycloak_grafana_client_secret"] == "" {
		updates["keycloak_grafana_client_secret"] = creds.grafanaSecret
	} else {
		creds.grafanaSecret = existing["keycloak_grafana_client_secret"]
	}
	if existing == nil || existing["keycloak_k8sadmin_password"] == "" {
		updates["keycloak_k8sadmin_password"] = creds.k8sAdminPassword
	} else {
		creds.k8sAdminPassword = existing["keycloak_k8sadmin_password"]
	}
	if existing == nil || existing["keycloak_developer_password"] == "" {
		updates["keycloak_developer_password"] = creds.developerPassword
	} else {
		creds.developerPassword = existing["keycloak_developer_password"]
	}

	if len(updates) > 0 {
		merged := map[string]string{}
		for key, val := range existing {
			merged[key] = val
		}
		for key, val := range updates {
			merged[key] = val
		}
		if werr := vault.WriteSecret(vaultPath, merged); werr != nil {
			fmt.Printf("Warning: could not write Keycloak secrets to Vault: %v\n", werr)
		} else {
			fmt.Printf("Keycloak secrets written to Vault at %s\n", vaultPath)
		}
	}

	return creds
}

func (k *Keycloak) Install() error {
	fmt.Println("Installing Keycloak (OIDC Identity Provider)...")

	cpIP := k.config.Network.ControlPlaneIP
	issuerURL := "https://keycloak.local/realms/k8s"

	creds := k.resolveCredentials()

	// Wait for VSO to sync the freshly-written Vault credentials into the keycloak-admin K8s
	// secret before creating the pod — env vars are captured at container start time and
	// Keycloak 26.x will not create the admin account if KEYCLOAK_ADMIN is empty.
	fmt.Println("Waiting for VSO to sync keycloak-admin secret...")
	if err := k.waitForAdminSecret(2 * time.Minute); err != nil {
		fmt.Printf("Warning: keycloak-admin secret may not be ready: %v\n", err)
	}

	fmt.Println("Deploying Keycloak...")
	if err := k.deployKeycloak(creds); err != nil {
		return err
	}

	fmt.Println("Waiting for Keycloak to be ready (first start includes build step, ~5-8 min)...")
	if err := k.waitForReady(20 * time.Minute); err != nil {
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
	creds := k.resolveCredentials()

	fmt.Println("Configuring Grafana OAuth2 with Keycloak...")
	var oauthErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if oauthErr = k.configureGrafanaOAuth(cpIP, creds); oauthErr == nil {
			break
		}
		fmt.Printf("Attempt %d/3 failed: %v — retrying in 20s...\n", attempt, oauthErr)
		time.Sleep(20 * time.Second)
	}
	return oauthErr
}

func (k *Keycloak) storeKubeconfigInVault(cpIP, issuerURL string) error {
	token := ResolveVaultToken(k.config.Vault.Token)
	if k.config.Vault.Addr == "" || token == "" {
		return fmt.Errorf("vault not configured")
	}

	kubeconfig := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: k8s-lab
  cluster:
    server: https://%s:6443
    insecure-skip-tls-verify: true
contexts:
- name: k8s-lab
  context:
    cluster: k8s-lab
    user: oidc
current-context: k8s-lab
users:
- name: oidc
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: kubectl
      args:
        - oidc-login
        - get-token
        - --oidc-issuer-url=%s
        - --oidc-client-id=kubectl
        - --oidc-pkce-method=auto
        - --insecure-skip-tls-verify
        - --listen-address=127.0.0.1:8000
`, cpIP, issuerURL)

	vault := NewVaultClient(k.config.Vault.Addr, token)
	if err := vault.WriteSecret("k8s-provisioner/kubeconfig-oidc", map[string]string{
		"config": kubeconfig,
	}); err != nil {
		return err
	}

	fmt.Println("kubeconfig-oidc stored at: secret/k8s-provisioner/kubeconfig-oidc")
	return nil
}

func (k *Keycloak) deployKeycloak(creds keycloakCreds) error {
	// Secrets keycloak-admin and postgres-credentials are managed by Vault Secrets Operator.
	// We only create the namespace here; the rest is deployed below.
	secrets := `apiVersion: v1
kind: Namespace
metadata:
  name: keycloak
  labels:
    istio-injection: enabled`

	rest := `
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: keycloak
  namespace: keycloak
automountServiceAccountToken: false
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: postgres
  namespace: keycloak
automountServiceAccountToken: false
---
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: postgres
  namespace: keycloak
spec:
  serviceName: postgres
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      serviceAccountName: postgres
      securityContext:
        runAsNonRoot: true
        runAsUser: 999
        fsGroup: 999
      containers:
      - name: postgres
        image: postgres:%s
        env:
        - name: POSTGRES_DB
          value: keycloak
        - name: POSTGRES_USER
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: username
        - name: POSTGRES_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: password
        - name: PGDATA
          value: /var/lib/postgresql/data/pgdata
        ports:
        - containerPort: 5432
          name: postgres
        volumeMounts:
        - name: data
          mountPath: /var/lib/postgresql/data
        readinessProbe:
          exec:
            command: ["pg_isready", "-U", "keycloak", "-d", "keycloak"]
          initialDelaySeconds: 10
          periodSeconds: 5
        resources:
          requests:
            memory: 256Mi
            cpu: 100m
          limits:
            memory: 512Mi
            cpu: 500m
  volumeClaimTemplates:
  - metadata:
      name: data
    spec:
      storageClassName: nfs-dynamic
      accessModes: ["ReadWriteOnce"]
      resources:
        requests:
          storage: 2Gi
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
  namespace: keycloak
spec:
  type: ClusterIP
  ports:
  - port: 5432
    targetPort: 5432
    name: postgres
  selector:
    app: postgres
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: keycloak
  namespace: keycloak
spec:
  replicas: 1
  selector:
    matchLabels:
      app: keycloak
  template:
    metadata:
      labels:
        app: keycloak
    spec:
      serviceAccountName: keycloak
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
      initContainers:
      - name: wait-for-postgres
        image: busybox:1.36
        command: ['sh', '-c', 'until nc -z postgres.keycloak.svc.cluster.local 5432; do echo waiting for postgres; sleep 3; done']
        securityContext:
          runAsNonRoot: true
          runAsUser: 65534
      containers:
      - name: keycloak
        image: quay.io/keycloak/keycloak:%s
        args:
        - start
        env:
        - name: KC_BOOTSTRAP_ADMIN_USERNAME
          valueFrom:
            secretKeyRef:
              name: keycloak-admin
              key: username
        - name: KC_BOOTSTRAP_ADMIN_PASSWORD
          valueFrom:
            secretKeyRef:
              name: keycloak-admin
              key: password
        - name: KC_DB
          value: postgres
        - name: KC_DB_URL
          value: jdbc:postgresql://postgres.keycloak.svc.cluster.local:5432/keycloak
        - name: KC_DB_USERNAME
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: username
        - name: KC_DB_PASSWORD
          valueFrom:
            secretKeyRef:
              name: postgres-credentials
              key: password
        - name: KC_HTTP_ENABLED
          value: "true"
        - name: KC_PROXY_HEADERS
          value: xforwarded
        - name: KC_HOSTNAME
          value: https://keycloak.local
        - name: KC_HOSTNAME_STRICT
          value: "false"
        - name: KC_HTTP_PORT
          value: "8080"
        - name: KC_HEALTH_ENABLED
          value: "true"
        ports:
        - containerPort: 8080
          name: http
        volumeMounts:
        - name: tmp
          mountPath: /tmp
        readinessProbe:
          httpGet:
            path: /health/ready
            port: 9000
          initialDelaySeconds: 60
          periodSeconds: 10
          failureThreshold: 15
        resources:
          requests:
            memory: 512Mi
            cpu: 250m
          limits:
            memory: 1Gi
            cpu: 1000m
      volumes:
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: keycloak
  namespace: keycloak
spec:
  type: NodePort
  ports:
  - port: 8080
    targetPort: 8080
    nodePort: 30080
    name: http
  selector:
    app: keycloak`

	pgVersion := k.config.Versions.Postgres
	if pgVersion == "" {
		pgVersion = "16"
	}
	kcVersion := k.config.Versions.Keycloak
	if kcVersion == "" {
		kcVersion = "26.2"
	}
	manifests := fmt.Sprintf(secrets+rest, pgVersion, kcVersion)

	if err := executor.WriteFile("/tmp/keycloak.yaml", manifests); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/keycloak.yaml")
	return err
}

func (k *Keycloak) waitForReady(timeout time.Duration) error {
	// Each phase gets its own deadline so a slow postgres (e.g. with Istio sidecar init)
	// cannot exhaust the budget before Keycloak itself is checked.
	pgDeadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(pgDeadline) {
		out, _ := k.exec.RunShell("kubectl get pods -n keycloak -l app=postgres -o jsonpath='{.items[0].status.containerStatuses[?(@.name==\"postgres\")].ready}' 2>/dev/null")
		if strings.TrimSpace(out) == "true" {
			fmt.Println("PostgreSQL is running!")
			break
		}
		fmt.Println("Waiting for PostgreSQL...")
		time.Sleep(DefaultPollInterval)
	}

	kcDeadline := time.Now().Add(timeout)
	for time.Now().Before(kcDeadline) {
		out, _ := k.exec.RunShell("kubectl get pods -n keycloak -l app=keycloak -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if strings.TrimSpace(out) == "Running" {
			break
		}
		fmt.Println("Waiting for Keycloak pod (includes build step)...")
		time.Sleep(DefaultPollInterval)
	}

	for time.Now().Before(kcDeadline) {
		out, _ := k.exec.RunShell("kubectl get pod -n keycloak -l app=keycloak -o jsonpath='{.items[0].status.containerStatuses[?(@.name==\"keycloak\")].ready}' 2>/dev/null")
		if strings.TrimSpace(out) == "true" {
			fmt.Println("Keycloak is ready!")
			break
		}
		fmt.Println("Waiting for Keycloak to be healthy...")
		time.Sleep(DefaultPollInterval)
	}

	// The readiness probe checks port 9000; port 8080 (Admin API) needs a brief extra warm-up.
	fmt.Println("Keycloak container is ready — waiting 30s for Admin API to initialize...")
	time.Sleep(30 * time.Second)
	fmt.Println("Keycloak Admin API should be ready, proceeding...")
	return nil
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
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("timeout: keycloak-admin secret not populated within %s", timeout)
}

func (k *Keycloak) configureRealm(cpIP string, creds keycloakCreds) error {
	script := fmt.Sprintf(`#!/bin/bash
set -e
KCADM=/opt/keycloak/bin/kcadm.sh
# Credentials are injected directly — no dependency on container env vars or VSO timing.
ADMIN_USER='%s'
ADMIN_PASS='%s'

echo "Authenticating to master realm..."
$KCADM config credentials --server http://localhost:8080 --realm master \
  --user "$ADMIN_USER" --password "$ADMIN_PASS"

echo "Creating k8s realm..."
if $KCADM get realms/k8s > /dev/null 2>&1; then
  echo "k8s realm already exists, skipping"
else
  $KCADM create realms -s realm=k8s -s enabled=true -s displayName=Kubernetes
fi

echo "Creating groups client scope..."
GROUPS_SCOPE_ID=$($KCADM create client-scopes -r k8s \
  -s name=groups \
  -s protocol=openid-connect \
  -s 'attributes={"include.in.token.scope":"true"}' \
  -i)

$KCADM create client-scopes/$GROUPS_SCOPE_ID/protocol-mappers/models -r k8s \
  -s name=groups \
  -s protocol=openid-connect \
  -s protocolMapper=oidc-group-membership-mapper \
  -s 'config={"full.path":"false","id.token.claim":"true","access.token.claim":"true","userinfo.token.claim":"true","claim.name":"groups"}'

echo "Creating kubectl client (public + PKCE)..."
KUBECTL_ID=$($KCADM create clients -r k8s \
  -s clientId=kubectl \
  -s publicClient=true \
  -s 'redirectUris=["http://localhost:8000/*","http://127.0.0.1:8000/*","http://localhost:18000/*"]' \
  -s 'attributes={"pkce.code.challenge.method":"S256"}' \
  -i)
$KCADM update clients/$KUBECTL_ID/optional-client-scopes/$GROUPS_SCOPE_ID -r k8s

$KCADM create clients/$KUBECTL_ID/protocol-mappers/models -r k8s \
  -s name=groups \
  -s protocol=openid-connect \
  -s protocolMapper=oidc-group-membership-mapper \
  -s 'config={"full.path":"false","id.token.claim":"true","access.token.claim":"true","userinfo.token.claim":"true","claim.name":"groups"}'

echo "Creating grafana client (confidential)..."
GRAFANA_ID=$($KCADM create clients -r k8s \
  -s clientId=grafana \
  -s publicClient=false \
  -s secret=%s \
  -s 'redirectUris=["https://grafana.local/*","http://grafana.local/*"]' \
  -i)

$KCADM update clients/$GRAFANA_ID/optional-client-scopes/$GROUPS_SCOPE_ID -r k8s

$KCADM create clients/$GRAFANA_ID/protocol-mappers/models -r k8s \
  -s name=groups \
  -s protocol=openid-connect \
  -s protocolMapper=oidc-group-membership-mapper \
  -s 'config={"full.path":"false","id.token.claim":"true","access.token.claim":"true","userinfo.token.claim":"true","claim.name":"groups"}'

echo "Creating groups..."
ADMINS_GID=$($KCADM create groups -r k8s -s name=k8s-admins -i)
DEVS_GID=$($KCADM create groups -r k8s -s name=k8s-developers -i)

echo "Creating k8sadmin user..."
ADMIN_UID=$($KCADM create users -r k8s \
  -s username=k8sadmin \
  -s email=k8sadmin@example.com \
  -s firstName=K8s \
  -s lastName=Admin \
  -s enabled=true \
  -i)
$KCADM set-password -r k8s --username k8sadmin --new-password '%s'
$KCADM update users/$ADMIN_UID/groups/$ADMINS_GID -r k8s \
  -s realm=k8s -s userId=$ADMIN_UID -s groupId=$ADMINS_GID -n

echo "Creating developer user..."
DEV_UID=$($KCADM create users -r k8s \
  -s username=developer \
  -s email=developer@example.com \
  -s firstName=Developer \
  -s lastName=User \
  -s enabled=true \
  -i)
$KCADM set-password -r k8s --username developer --new-password '%s'
$KCADM update users/$DEV_UID/groups/$DEVS_GID -r k8s \
  -s realm=k8s -s userId=$DEV_UID -s groupId=$DEVS_GID -n

echo "Keycloak realm configuration completed!"
`, creds.adminUsername, creds.adminPassword, creds.grafanaSecret, creds.k8sAdminPassword, creds.developerPassword)

	pod, err := k.exec.RunShell("kubectl get pods -n keycloak -l app=keycloak -o jsonpath='{.items[0].metadata.name}'")
	if err != nil {
		return err
	}
	pod = strings.TrimSpace(pod)

	_, err = k.exec.RunShellWithStdin(fmt.Sprintf("kubectl exec -i -n keycloak %s -c keycloak -- bash -s", pod), script)
	return err
}

func (k *Keycloak) patchAPIServer(issuerURL string) error {
	authConfig := fmt.Sprintf(`apiVersion: apiserver.config.k8s.io/v1beta1
kind: AuthenticationConfiguration
jwt:
- issuer:
    url: %s
    audiences:
    - kubectl
    - account
    audienceMatchPolicy: MatchAny
  claimMappings:
    username:
      claim: preferred_username
      prefix: "oidc:"
    groups:
      claim: groups
      prefix: "oidc:"
`, issuerURL)

	if err := executor.WriteFile("/etc/kubernetes/pki/auth-config.yaml", authConfig); err != nil {
		return err
	}

	// Skip if already patched
	out, _ := k.exec.RunShell("grep -c 'authentication-config' /etc/kubernetes/manifests/kube-apiserver.yaml 2>/dev/null || echo 0")
	if strings.TrimSpace(out) != "0" {
		fmt.Println("API server already configured with authentication-config, skipping")
	} else {
		patchCmd := `sed -i '/- kube-apiserver/a\    - --authentication-config=/etc/kubernetes/pki/auth-config.yaml' /etc/kubernetes/manifests/kube-apiserver.yaml`
		if _, err := k.exec.RunShell(patchCmd); err != nil {
			return err
		}

		fmt.Println("Waiting for API server to restart with OIDC config...")
		time.Sleep(20 * time.Second)

		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			out, err := k.exec.RunShell("kubectl get --raw='/healthz' 2>/dev/null")
			if err == nil && strings.Contains(out, "ok") {
				fmt.Println("API server is back online!")
				break
			}
			time.Sleep(DefaultPollInterval)
		}
	}

	rbac := `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: oidc-k8s-admins
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
subjects:
- kind: Group
  name: "oidc:k8s-admins"
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: oidc-k8s-developers
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: view
subjects:
- kind: Group
  name: "oidc:k8s-developers"
  apiGroup: rbac.authorization.k8s.io`

	if err := executor.WriteFile("/tmp/oidc-rbac.yaml", rbac); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/oidc-rbac.yaml")
	return err
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
		time.Sleep(DefaultPollInterval)
	}
	return fmt.Errorf("timeout waiting for secret %s/%s", namespace, name)
}

func (k *Keycloak) configureGrafanaOAuth(cpIP string, creds keycloakCreds) error {
	// grafana-oidc is synced by VSO from Vault; Grafana pod won't start without it.
	if err := k.waitForSecret("monitoring", "grafana-oidc", 3*time.Minute); err != nil {
		return fmt.Errorf("grafana-oidc secret not ready: %w", err)
	}

	iniLines := []string{
		"[auth.generic_oauth]",
		"enabled = true",
		"name = Keycloak",
		"allow_sign_up = true",
		"auto_login = false",
		"client_id = grafana",
		"client_secret = ${GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET}",
		"scopes = openid email profile groups",
		"auth_url = https://keycloak.local/realms/k8s/protocol/openid-connect/auth",
		"token_url = http://keycloak.keycloak.svc.cluster.local:8080/realms/k8s/protocol/openid-connect/token",
		"api_url = http://keycloak.keycloak.svc.cluster.local:8080/realms/k8s/protocol/openid-connect/userinfo",
		"redirect_uri = https://grafana.local/login/generic_oauth",
		"role_attribute_path = contains(groups[*], 'k8s-admins') && 'Admin' || 'Viewer'",
		"role_attribute_strict = true",
		"allow_assign_grafana_admin = true",
		"tls_skip_verify_insecure = true",
		"",
		"[server]",
		"domain = grafana.local",
		"root_url = https://grafana.local/",
		"serve_from_sub_path = false",
	}

	var indented strings.Builder
	for _, line := range iniLines {
		indented.WriteString("    ")
		indented.WriteString(line)
		indented.WriteString("\n")
	}

	// grafana-oidc Secret is managed by Vault Secrets Operator; only apply the ConfigMap.
	resources := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-ini
  namespace: monitoring
data:
  grafana.ini: |
%s`, indented.String())

	if err := executor.WriteFile("/tmp/grafana-keycloak.yaml", resources); err != nil {
		return err
	}

	if _, err := k.exec.RunShell("kubectl apply -f /tmp/grafana-keycloak.yaml"); err != nil {
		return err
	}

	// Patch Grafana deployment: add volume, volumeMount, env var (skip if already applied)
	alreadyPatched, _ := k.exec.RunShell(`kubectl get deployment grafana -n monitoring -o jsonpath='{.spec.template.spec.volumes[?(@.name=="grafana-ini")].name}' 2>/dev/null`)
	if strings.TrimSpace(alreadyPatched) != "grafana-ini" {
		patch := `[
  {"op":"add","path":"/spec/template/spec/volumes/-","value":{"name":"grafana-ini","configMap":{"name":"grafana-ini"}}},
  {"op":"add","path":"/spec/template/spec/containers/0/volumeMounts/-","value":{"name":"grafana-ini","mountPath":"/etc/grafana/grafana.ini","subPath":"grafana.ini"}},
  {"op":"add","path":"/spec/template/spec/containers/0/env/-","value":{"name":"GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET","valueFrom":{"secretKeyRef":{"name":"grafana-oidc","key":"client-secret"}}}}
]`
		if err := executor.WriteFile("/tmp/grafana-oidc-patch.json", patch); err != nil {
			return err
		}
		if _, err := k.exec.RunShell("kubectl patch deployment grafana -n monitoring --type=json --patch-file=/tmp/grafana-oidc-patch.json"); err != nil {
			return err
		}
	} else {
		fmt.Println("Grafana deployment already patched for OAuth, skipping")
	}

	_, err := k.exec.RunShell("kubectl rollout restart deployment/grafana -n monitoring")
	return err
}

func (k *Keycloak) createGateway() error {
	gateway := `apiVersion: networking.istio.io/v1beta1
kind: Gateway
metadata:
  name: keycloak-gateway
  namespace: keycloak
spec:
  selector:
    istio: ingressgateway
  servers:
  - port:
      number: 80
      name: http
      protocol: HTTP
    hosts:
    - keycloak.local
    tls:
      httpsRedirect: true
  - port:
      number: 443
      name: https
      protocol: HTTPS
    tls:
      mode: SIMPLE
      credentialName: lab-tls-secret
    hosts:
    - keycloak.local
---
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata:
  name: keycloak
  namespace: keycloak
spec:
  hosts:
  - keycloak.local
  gateways:
  - keycloak-gateway
  http:
  - route:
    - destination:
        host: keycloak.keycloak.svc.cluster.local
        port:
          number: 8080`

	if err := executor.WriteFile("/tmp/keycloak-gateway.yaml", gateway); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/keycloak-gateway.yaml")
	return err
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
    --exec-arg=--listen-address=127.0.0.1:8000
`, issuerURL)
	fmt.Println("\nTest login:")
	fmt.Println("  kubectl get nodes --user=oidc")
	fmt.Println("\n--- Grafana SSO ---")
	fmt.Println("  Grafana now uses Keycloak for login.")
	fmt.Println("  Local admin login still works: admin / admin123")
	fmt.Println("========================================")
}