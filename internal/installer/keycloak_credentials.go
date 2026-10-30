package installer

import (
	"fmt"
	"os"
)

// keycloakCredsFile is where generated credentials are written (0600) when Vault
// is not configured, so they are not echoed into provisioning/CI logs.
const keycloakCredsFile = "/etc/k8s-provisioner/keycloak-credentials.txt"

func (k *Keycloak) resolveCredentials() (keycloakCreds, error) {
	// Passwords/secrets are random, never hardcoded constants. Without Vault
	// these generated values are used and recorded once below; with Vault each
	// comes from its own path (see labCredential) and these are only the
	// fallback when Vault cannot be read.
	gen := map[string]string{}
	for _, name := range []string{"admin", "postgres", "grafana", "k8sadmin", "developer"} {
		val, err := generatePassword(20)
		if err != nil {
			return keycloakCreds{}, fmt.Errorf("generate %s credential: %w", name, err)
		}
		gen[name] = val
	}

	generated := keycloakCreds{
		adminUsername:     "admin",
		adminPassword:     gen["admin"],
		postgresUsername:  "keycloak",
		postgresPassword:  gen["postgres"],
		grafanaSecret:     gen["grafana"],
		k8sAdminPassword:  gen["k8sadmin"],
		developerPassword: gen["developer"],
	}
	creds := generated

	resolver := NewSecretResolver(k.config)
	if !resolver.Enabled() {
		fmt.Println("Warning: Vault not configured — generated random Keycloak credentials.")
		contents := fmt.Sprintf(
			"keycloak admin: %s / %s\npostgres:       %s / %s\nk8s-admin OIDC: %s\ndeveloper OIDC: %s\n",
			creds.adminUsername, creds.adminPassword,
			creds.postgresUsername, creds.postgresPassword,
			creds.k8sAdminPassword, creds.developerPassword,
		)
		// Prefer a 0600 file over stdout so secrets don't end up in CI/provisioning
		// logs. Fall back to stdout only if the file cannot be written — otherwise
		// these unrecoverable credentials would be lost entirely.
		if err := os.WriteFile(keycloakCredsFile, []byte(contents), 0600); err != nil {
			fmt.Printf("Warning: could not write %s (%v) — printing credentials once instead.\n", keycloakCredsFile, err)
			fmt.Println("  SAVE THESE NOW (they are not persisted anywhere):")
			fmt.Print("    " + contents)
		} else {
			fmt.Printf("  Credentials written to %s (mode 0600) — back them up; they are not stored in Vault.\n", keycloakCredsFile)
		}
		return creds, nil
	}

	// Vault holds each login at its own path (seeded by the Vault step); any
	// missing value is generated and written back here.
	vault := resolver.Client()
	for _, f := range []struct {
		cred          labCredential
		login, secret *string
	}{
		{keycloakAdminCredential, &creds.adminUsername, &creds.adminPassword},
		{keycloakPostgresCredential, &creds.postgresUsername, &creds.postgresPassword},
		{keycloakGrafanaClientCredential, nil, &creds.grafanaSecret},
		{keycloakK8sAdminCredential, nil, &creds.k8sAdminPassword},
		{keycloakDeveloperCredential, nil, &creds.developerPassword},
	} {
		login, secret, err := ensureCredential(vault, f.cred, nil)
		if err != nil {
			fmt.Printf("Warning: could not read Keycloak credentials from Vault: %v — using generated values (not persisted)\n", err)
			return generated, nil
		}
		if f.login != nil {
			*f.login = login
		}
		*f.secret = secret
	}
	return creds, nil
}
