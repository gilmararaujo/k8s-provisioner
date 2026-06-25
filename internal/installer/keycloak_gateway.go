package installer

import "github.com/techiescamp/k8s-provisioner/internal/executor"

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

// createPostgresMTLS requires mTLS on the Postgres workload so the Keycloak→Postgres
// connection (plaintext JDBC, no sslmode) is encrypted by the mesh. The policy is
// scoped to the postgres pods via selector — Postgres' only clients are the
// mesh-injected Keycloak pod and the kubelet `exec` readiness probe (network-
// independent), so STRICT here has near-zero blast radius. Only meaningful with Istio;
// the caller gates this on ServiceMesh == "istio".
func (k *Keycloak) createPostgresMTLS() error {
	manifest := `apiVersion: security.istio.io/v1
kind: PeerAuthentication
metadata:
  name: postgres-mtls
  namespace: keycloak
spec:
  selector:
    matchLabels:
      app: postgres
  mtls:
    mode: STRICT`

	if err := executor.WriteFile("/tmp/keycloak-postgres-mtls.yaml", manifest); err != nil {
		return err
	}

	_, err := k.exec.RunShell("kubectl apply -f /tmp/keycloak-postgres-mtls.yaml")
	return err
}
