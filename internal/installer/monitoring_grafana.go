package installer

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

func (m *Monitoring) installGrafana() error {
	password, err := m.resolveGrafanaPassword()
	if err != nil {
		return err
	}

	// Cria K8s Secret com a senha (do Vault ou fallback)
	if err := m.createGrafanaSecret(password); err != nil {
		return err
	}

	grafana := `apiVersion: v1
kind: ServiceAccount
metadata:
  name: grafana
  namespace: monitoring
automountServiceAccountToken: false
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-datasources
  namespace: monitoring
data:
  datasources.yaml: |
    apiVersion: 1
    datasources:
    - name: Prometheus
      type: prometheus
      access: proxy
      url: http://prometheus:9090
      isDefault: true
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: grafana
  namespace: monitoring
spec:
  replicas: 1
  selector:
    matchLabels:
      app: grafana
  template:
    metadata:
      labels:
        app: grafana
        version: "%s"
    spec:
      serviceAccountName: grafana
      securityContext:
        runAsNonRoot: true
        runAsUser: 472
        runAsGroup: 472
        fsGroup: 472
      containers:
      - name: grafana
        image: grafana/grafana:%s
        imagePullPolicy: IfNotPresent
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: [ALL]
        ports:
        - containerPort: 3000
        env:
        - name: GF_SECURITY_ADMIN_USER
          value: admin
        - name: GF_SECURITY_ADMIN_PASSWORD
          valueFrom:
            secretKeyRef:
              name: grafana-admin
              key: password
        - name: GF_USERS_ALLOW_SIGN_UP
          value: "false"
        volumeMounts:
        - name: datasources
          mountPath: /etc/grafana/provisioning/datasources
        - name: grafana-data
          mountPath: /var/lib/grafana
        - name: grafana-logs
          mountPath: /var/log/grafana
        - name: tmp
          mountPath: /tmp
        resources:
          requests:
            memory: 256Mi
            cpu: 100m
          limits:
            memory: 512Mi
            cpu: 500m
      volumes:
      - name: datasources
        configMap:
          name: grafana-datasources
      - name: grafana-data
        emptyDir: {}
      - name: grafana-logs
        emptyDir: {}
      - name: tmp
        emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: grafana
  namespace: monitoring
spec:
  type: ClusterIP
  ports:
  - port: 3000
    targetPort: 3000
  selector:
    app: grafana`

	version := m.config.Versions.Grafana
	if version == "" {
		version = "13.0.1"
	}
	grafana = fmt.Sprintf(grafana, version, version)

	if err := executor.WriteFile("/tmp/grafana.yaml", grafana); err != nil {
		return err
	}

	_, err = m.exec.RunShell("kubectl apply -f /tmp/grafana.yaml")
	return err
}

// resolveGrafanaPassword returns the Grafana admin password from Vault, or a
// freshly generated random password (never a hardcoded default) when Vault is
// disabled or the key is missing. A generated password is printed once, since it
// is not persisted anywhere in that mode.
func (m *Monitoring) resolveGrafanaPassword() (string, error) {
	resolver := NewSecretResolver(m.config)
	if resolver.Enabled() {
		if pw := resolver.Resolve("Grafana password", "", "grafana_admin_password"); pw != "" {
			return pw, nil
		}
	}
	pw, err := generatePassword(20)
	if err != nil {
		return "", fmt.Errorf("generate grafana password: %w", err)
	}
	fmt.Println("Warning: Grafana admin password not in Vault — generated a random one.")
	fmt.Printf("  SAVE THIS NOW (not persisted): admin / %s\n", pw)
	return pw, nil
}

func (m *Monitoring) createGrafanaSecret(password string) error {
	// Skip if already managed by Vault Secrets Operator
	if out, _ := m.exec.RunShell("kubectl get secret grafana-admin -n monitoring -o name 2>/dev/null"); out != "" {
		fmt.Println("Grafana admin secret already synced by Vault Secrets Operator, skipping direct creation")
		return nil
	}
	// Build the Secret as a manifest and pipe it via stdin so the password is
	// never interpolated into a shell command (no injection, no leak in ps/logs).
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: grafana-admin
  namespace: monitoring
type: Opaque
stringData:
  password: %q
`, password)
	if _, err := m.exec.RunShellWithStdin("kubectl apply -f -", manifest); err != nil {
		return fmt.Errorf("failed to create grafana-admin secret: %w", err)
	}
	fmt.Println("Grafana admin secret created")
	return nil
}
