package installer

import "fmt"

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
		fmt.Println("  SAVE THESE NOW (they are not persisted anywhere):")
		fmt.Printf("    keycloak admin: %s / %s\n", creds.adminUsername, creds.adminPassword)
		fmt.Printf("    postgres:       %s / %s\n", creds.postgresUsername, creds.postgresPassword)
		fmt.Printf("    k8s-admin OIDC: %s\n", creds.k8sAdminPassword)
		fmt.Printf("    developer OIDC: %s\n", creds.developerPassword)
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
