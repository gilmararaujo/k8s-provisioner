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
	adminPassword    string
	postgresPassword string
	grafanaSecret    string
}

func NewKeycloak(cfg *config.Config, exec executor.CommandExecutor) *Keycloak {
	return &Keycloak{config: cfg, exec: exec}
}

func (k *Keycloak) resolveCredentials() keycloakCreds {
	creds := keycloakCreds{
		adminPassword:    "Admin@Keycloak123",
		postgresPassword: "Keycloak@Pg123",
		grafanaSecret:    "grafana-oidc-secret",
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
	if existing == nil || existing["keycloak_admin_password"] == "" {
		updates["keycloak_admin_password"] = creds.adminPassword
	} else {
		creds.adminPassword = existing["keycloak_admin_password"]
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
	issuerURL := fmt.Sprintf("http://%s:30080/realms/k8s", cpIP)

	creds := k.resolveCredentials()

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
		fmt.Printf("Warning: realm configuration failed: %v\n", err)
	}

	fmt.Println("Patching API server with OIDC authentication...")
	if err := k.patchAPIServer(issuerURL); err != nil {
		fmt.Printf("Warning: API server patch failed: %v\n", err)
	}

	if k.config.Components.Monitoring == "prometheus-stack" {
		fmt.Println("Configuring Grafana OAuth2 with Keycloak...")
		if err := k.configureGrafanaOAuth(cpIP, creds); err != nil {
			fmt.Printf("Warning: Grafana OAuth2 configuration failed: %v\n", err)
		}
	}

	if k.config.Components.ServiceMesh == "istio" {
		fmt.Println("Creating Istio Gateway for Keycloak...")
		if err := k.createGateway(); err != nil {
			fmt.Printf("Warning: Failed to create Keycloak gateway: %v\n", err)
		}
	}

	fmt.Println("Keycloak installed successfully!")
	k.printAccessInfo(cpIP, issuerURL)
	return nil
}

func (k *Keycloak) deployKeycloak(creds keycloakCreds) error {
	secrets := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: keycloak
---
apiVersion: v1
kind: Secret
metadata:
  name: keycloak-admin
  namespace: keycloak
type: Opaque
stringData:
  username: admin
  password: %s
---
apiVersion: v1
kind: Secret
metadata:
  name: postgres-credentials
  namespace: keycloak
type: Opaque
stringData:
  username: keycloak
  password: %s
`, creds.adminPassword, creds.postgresPassword)

	rest := `---
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
        image: postgres:16
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
        image: quay.io/keycloak/keycloak:26.2
        args:
        - start
        env:
        - name: KEYCLOAK_ADMIN
          valueFrom:
            secretKeyRef:
              name: keycloak-admin
              key: username
        - name: KEYCLOAK_ADMIN_PASSWORD
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
          value: keycloak.local
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

	manifests := secrets + rest

	if err := executor.WriteFile("/tmp/keycloak.yaml", manifests); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/keycloak.yaml")
	return err
}

func (k *Keycloak) waitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Wait for PostgreSQL
	for time.Now().Before(deadline) {
		out, _ := k.exec.RunShell("kubectl get pods -n keycloak -l app=postgres -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out == "Running" {
			fmt.Println("PostgreSQL is running!")
			break
		}
		fmt.Println("Waiting for PostgreSQL...")
		time.Sleep(DefaultPollInterval)
	}

	// Wait for Keycloak pod to be Running
	for time.Now().Before(deadline) {
		out, _ := k.exec.RunShell("kubectl get pods -n keycloak -l app=keycloak -o jsonpath='{.items[0].status.phase}' 2>/dev/null")
		if out == "Running" {
			break
		}
		fmt.Println("Waiting for Keycloak pod (includes build step)...")
		time.Sleep(DefaultPollInterval)
	}

	// Wait for Keycloak health endpoint
	for time.Now().Before(deadline) {
		_, err := k.exec.RunShell("kubectl get pod -n keycloak -l app=keycloak -o jsonpath='{.items[0].status.containerStatuses[0].ready}' 2>/dev/null | grep -q true")
		if err == nil {
			fmt.Println("Keycloak is ready!")
			return nil
		}
		fmt.Println("Waiting for Keycloak to be healthy...")
		time.Sleep(DefaultPollInterval)
	}

	return fmt.Errorf("timeout waiting for Keycloak")
}

func (k *Keycloak) configureRealm(cpIP string, creds keycloakCreds) error {
	script := fmt.Sprintf(`#!/bin/bash
set -e
KCADM=/opt/keycloak/bin/kcadm.sh

echo "Authenticating to master realm..."
$KCADM config credentials --server http://localhost:8080 --realm master \
  --user "$KEYCLOAK_ADMIN" --password "$KEYCLOAK_ADMIN_PASSWORD"

echo "Creating k8s realm..."
$KCADM create realms -s realm=k8s -s enabled=true -s displayName=Kubernetes || echo "Realm may already exist"

echo "Creating kubectl client (public + PKCE)..."
KUBECTL_ID=$($KCADM create clients -r k8s \
  -s clientId=kubectl \
  -s publicClient=true \
  -s 'redirectUris=["http://localhost:8000","http://localhost:18000"]' \
  -s 'attributes={"pkce.code.challenge.method":"S256"}' \
  -i)

$KCADM create clients/$KUBECTL_ID/protocol-mappers/models -r k8s \
  -s name=groups \
  -s protocol=openid-connect \
  -s protocolMapper=oidc-group-membership-mapper \
  -s 'config.full.path=false' \
  -s 'config.id.token.claim=true' \
  -s 'config.access.token.claim=true' \
  -s 'config.userinfo.token.claim=true' \
  -s 'config.claim.name=groups'

echo "Creating grafana client (confidential)..."
GRAFANA_ID=$($KCADM create clients -r k8s \
  -s clientId=grafana \
  -s publicClient=false \
  -s secret=%s \
  -s 'redirectUris=["https://grafana.local/*","http://grafana.local/*"]' \
  -i)

$KCADM create clients/$GRAFANA_ID/protocol-mappers/models -r k8s \
  -s name=groups \
  -s protocol=openid-connect \
  -s protocolMapper=oidc-group-membership-mapper \
  -s 'config.full.path=false' \
  -s 'config.id.token.claim=true' \
  -s 'config.access.token.claim=true' \
  -s 'config.userinfo.token.claim=true' \
  -s 'config.claim.name=groups'

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
$KCADM set-password -r k8s --username k8sadmin --new-password 'Admin@K8s123'
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
$KCADM set-password -r k8s --username developer --new-password 'Dev@K8s123'
$KCADM update users/$DEV_UID/groups/$DEVS_GID -r k8s \
  -s realm=k8s -s userId=$DEV_UID -s groupId=$DEVS_GID -n

echo "Keycloak realm configuration completed!"
`, creds.grafanaSecret, cpIP)

	if err := executor.WriteFile("/tmp/setup-keycloak.sh", script); err != nil {
		return err
	}

	pod, err := k.exec.RunShell("kubectl get pods -n keycloak -l app=keycloak -o jsonpath='{.items[0].metadata.name}'")
	if err != nil {
		return err
	}
	pod = strings.TrimSpace(pod)

	if _, err := k.exec.RunShell(fmt.Sprintf("kubectl cp /tmp/setup-keycloak.sh keycloak/%s:/tmp/setup-keycloak.sh", pod)); err != nil {
		return err
	}

	_, err = k.exec.RunShell(fmt.Sprintf("kubectl exec -n keycloak %s -- bash /tmp/setup-keycloak.sh", pod))
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

func (k *Keycloak) configureGrafanaOAuth(cpIP string, creds keycloakCreds) error {
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
		"token_url = https://keycloak.local/realms/k8s/protocol/openid-connect/token",
		"api_url = https://keycloak.local/realms/k8s/protocol/openid-connect/userinfo",
		"redirect_uri = https://grafana.local/login/generic_oauth",
		"role_attribute_path = contains(groups[*], 'k8s-admins') && 'Admin' || 'Viewer'",
		"tls_skip_verify_insecure = true",
	}

	var indented strings.Builder
	for _, line := range iniLines {
		indented.WriteString("    ")
		indented.WriteString(line)
		indented.WriteString("\n")
	}

	resources := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-ini
  namespace: monitoring
data:
  grafana.ini: |
%s---
apiVersion: v1
kind: Secret
metadata:
  name: grafana-oidc
  namespace: monitoring
type: Opaque
stringData:
  client-secret: %s`, indented.String(), creds.grafanaSecret)

	if err := executor.WriteFile("/tmp/grafana-keycloak.yaml", resources); err != nil {
		return err
	}

	if _, err := k.exec.RunShell("kubectl apply -f /tmp/grafana-keycloak.yaml"); err != nil {
		return err
	}

	// Patch Grafana deployment: add volume, volumeMount, env var
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
	fmt.Println("\nAdmin credentials:")
	fmt.Println("  User:     admin")
	fmt.Println("  Password: Admin@Keycloak123")
	fmt.Println("\nTest users (realm: k8s):")
	fmt.Println("  k8sadmin  / Admin@K8s123  (group: k8s-admins  → cluster-admin)")
	fmt.Println("  developer / Dev@K8s123    (group: k8s-developers → view)")
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
    --exec-arg=--oidc-use-pkce
`, issuerURL)
	fmt.Println("\nTest login:")
	fmt.Println("  kubectl get nodes --user=oidc")
	fmt.Println("\n--- Grafana SSO ---")
	fmt.Println("  Grafana now uses Keycloak for login.")
	fmt.Println("  Local admin login still works: admin / admin123")
	fmt.Println("========================================")
}