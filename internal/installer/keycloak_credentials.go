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
	seedOrRead(existing, updates, "keycloak_admin_username", &creds.adminUsername)
	seedOrRead(existing, updates, "keycloak_admin_password", &creds.adminPassword)
	seedOrRead(existing, updates, "keycloak_postgres_username", &creds.postgresUsername)
	seedOrRead(existing, updates, "keycloak_postgres_password", &creds.postgresPassword)
	seedOrRead(existing, updates, "keycloak_grafana_client_secret", &creds.grafanaSecret)
	seedOrRead(existing, updates, "keycloak_k8sadmin_password", &creds.k8sAdminPassword)
	seedOrRead(existing, updates, "keycloak_developer_password", &creds.developerPassword)

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

// seedOrRead reconciles one secret with Vault: if Vault already holds a non-empty
// value at key, adopt it into *current; otherwise stage *current (the generated
// default) in updates to be written back. existing may be nil.
func seedOrRead(existing, updates map[string]string, key string, current *string) {
	if v := existing[key]; v != "" {
		*current = v
		return
	}
	updates[key] = *current
}
