package installer

import (
	"fmt"
	"strings"
	"time"

	"github.com/techiescamp/k8s-provisioner/internal/executor"
)

func (k *Keycloak) configureGrafanaOAuth(cpIP string, creds keycloakCreds) error {
	// grafana-oidc is synced by VSO from Vault; Grafana pod won't start without it.
	if err := k.waitForSecret("monitoring", "grafana-oidc", 3*time.Minute); err != nil {
		return fmt.Errorf("grafana-oidc secret not ready: %w", err)
	}

	// Lab CA that signs https://keycloak.local, so Grafana can verify TLS to the
	// OIDC issuer instead of skipping verification.
	labCA, err := k.labCABase64()
	if err != nil {
		return fmt.Errorf("read lab CA for Grafana OIDC TLS: %w", err)
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
		"tls_client_ca = /etc/grafana/keycloak-ca/ca.crt",
		// PKCE hardens the authorization-code exchange against code interception
		// (defense in depth even for this confidential client); use_refresh_token lets
		// Grafana refresh the session silently instead of leaning on a longer-lived one.
		"use_pkce = true",
		"use_refresh_token = true",
		"",
		"[server]",
		"domain = grafana.local",
		"root_url = https://grafana.local/",
		"serve_from_sub_path = false",
		"",
		// Harden the grafana_session cookie and browser transport. Grafana is served
		// over HTTPS via the Istio gateway, so the session cookie must carry Secure;
		// CSP + HSTS add clickjacking/XSS and transport-downgrade protection that are
		// otherwise absent. cookie_samesite=lax is the working default for the OAuth
		// redirect flow (strict would break the cross-site login callback).
		"[security]",
		"cookie_secure = true",
		"cookie_samesite = lax",
		"content_security_policy = true",
		"strict_transport_security = true",
	}

	var indented strings.Builder
	for _, line := range iniLines {
		indented.WriteString("    ")
		indented.WriteString(line)
		indented.WriteString("\n")
	}

	// grafana-oidc Secret is managed by Vault Secrets Operator; only apply the ConfigMaps.
	// keycloak-ca carries the lab CA (binaryData decodes the base64 PEM to a file)
	// so Grafana can verify TLS to the OIDC issuer.
	resources := fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: grafana-ini
  namespace: monitoring
data:
  grafana.ini: |
%s
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: keycloak-ca
  namespace: monitoring
binaryData:
  ca.crt: %s`, indented.String(), labCA)

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
  {"op":"add","path":"/spec/template/spec/volumes/-","value":{"name":"keycloak-ca","configMap":{"name":"keycloak-ca"}}},
  {"op":"add","path":"/spec/template/spec/containers/0/volumeMounts/-","value":{"name":"grafana-ini","mountPath":"/etc/grafana/grafana.ini","subPath":"grafana.ini"}},
  {"op":"add","path":"/spec/template/spec/containers/0/volumeMounts/-","value":{"name":"keycloak-ca","mountPath":"/etc/grafana/keycloak-ca"}},
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

	_, err = k.exec.RunShell("kubectl rollout restart deployment/grafana -n monitoring")
	return err
}
