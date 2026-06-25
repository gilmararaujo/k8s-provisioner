package installer

import (
	"fmt"
	"strings"
)

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

# Set explicit session/token lifespans so the timeout is managed by this tool
# rather than inherited from whatever the Keycloak image defaults to. Values
# mirror sane Keycloak defaults: 30m SSO idle, 10h SSO max, 5m access token.
# Run unconditionally (update is idempotent) so it applies to a pre-existing realm too.
echo "Setting realm session/token lifespans..."
$KCADM update realms/k8s \
  -s ssoSessionIdleTimeout=1800 \
  -s ssoSessionMaxLifespan=36000 \
  -s accessTokenLifespan=300

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
