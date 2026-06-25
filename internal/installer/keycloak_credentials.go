package installer

import (
	"fmt"
	"os"
)

// keycloakCredsFile is where generated credentials are written (0600) when Vault
// is not configured, so they are not echoed into provisioning/CI logs.
const keycloakCredsFile = "/etc/k8s-provisioner/keycloak-credentials.txt"

func (k *Keycloak) resolveCredentials() (keycloakCreds, error) {
	// Passwords/secrets default to freshly generated random values, never
	// hardcoded constants. With Vault enabled these act as seeds for any missing
	// key and are persisted (recoverable via `k8s-provisioner vault get`); without
	// Vault they are printed once below so the operator can record them.
	gen := map[string]string{}
	for _, name := range []string{"admin", "postgres", "grafana", "k8sadmin", "developer"} {
		val, err := generatePassword(20)
		if err != nil {
			return keycloakCreds{}, fmt.Errorf("generate %s credential: %w", name, err)
		}
		gen[name] = val
	}

	creds := keycloakCreds{
		adminUsername:     "admin",
		adminPassword:     gen["admin"],
		postgresUsername:  "keycloak",
		postgresPassword:  gen["postgres"],
		grafanaSecret:     gen["grafana"],
		k8sAdminPassword:  gen["k8sadmin"],
		developerPassword: gen["developer"],
	}

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

	vault := resolver.Client()
	const vaultPath = apiKeysPath

	existing, err := vault.ReadSecret(vaultPath)
	if err != nil {
		fmt.Printf("Warning: could not read Vault secrets: %v — using generated values (not persisted)\n", err)
		return creds, nil
	}

	// For each secret: adopt the existing Vault value if present, else stage our
	// generated value to be written back. (Indexing a nil map yields "", so no
	// nil check is needed.)
	updates := map[string]string{}
	creds.adminUsername = resolveSecret(existing, updates, "keycloak_admin_username", creds.adminUsername)
	creds.adminPassword = resolveSecret(existing, updates, "keycloak_admin_password", creds.adminPassword)
	creds.postgresUsername = resolveSecret(existing, updates, "keycloak_postgres_username", creds.postgresUsername)
	creds.postgresPassword = resolveSecret(existing, updates, "keycloak_postgres_password", creds.postgresPassword)
	creds.grafanaSecret = resolveSecret(existing, updates, "keycloak_grafana_client_secret", creds.grafanaSecret)
	creds.k8sAdminPassword = resolveSecret(existing, updates, "keycloak_k8sadmin_password", creds.k8sAdminPassword)
	creds.developerPassword = resolveSecret(existing, updates, "keycloak_developer_password", creds.developerPassword)

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

	return creds, nil
}

// resolveSecret reconciles one secret with Vault: if Vault already holds a
// non-empty value at key, that value is returned; otherwise generated (our
// generated default) is staged in updates to be written back and returned.
// existing may be nil (indexing a nil map yields "").
func resolveSecret(existing, updates map[string]string, key, generated string) string {
	if v := existing[key]; v != "" {
		return v
	}
	updates[key] = generated
	return generated
}
