package installer

import (
	"fmt"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

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
          value: "true"
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
  type: ClusterIP
  ports:
  - port: 8080
    targetPort: 8080
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
