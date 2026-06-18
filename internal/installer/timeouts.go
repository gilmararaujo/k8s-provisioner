package installer

import "time"

// Timeout constants for installer operations
const (
	// Default timeout for waiting for components to be ready
	defaultReadyTimeout = 5 * time.Minute

	// Shorter timeout for lighter components
	shortReadyTimeout = 3 * time.Minute

	// Poll intervals for checking component status
	defaultPollInterval = 10 * time.Second
	shortPollInterval   = 5 * time.Second
	longPollInterval    = 15 * time.Second

	// Initial delays before checking status
	crdInitialDelay       = 20 * time.Second
	metalLBConfigureDelay = 30 * time.Second
	monitoringInitDelay   = 15 * time.Second

	// Component-specific waits (named so the intent is in the constant, not an
	// inline literal — see NM-4).
	keycloakStartTimeout   = 20 * time.Minute // first start includes a build step
	adminSecretSyncTimeout = 2 * time.Minute  // VSO sync of the keycloak-admin secret
	oauthRetryDelay        = 20 * time.Second  // backoff between Grafana OAuth attempts
	apiServerRestartWait   = 20 * time.Second  // settle time before polling /healthz
	apiServerHealthTimeout = 2 * time.Minute   // apiserver back-online after OIDC patch
	webhookRegisterWait    = 10 * time.Second  // let the cert-manager webhook register
	caSecretWaitTimeout    = 60 * time.Second  // wait for the lab CA secret to exist
	certReadyTimeout       = 2 * time.Minute   // wait for the lab TLS certificate
	vaultReadyTimeout      = 3 * time.Minute   // wait for Vault to be reachable
)
