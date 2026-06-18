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
