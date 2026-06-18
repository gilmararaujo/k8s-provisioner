package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestSecretResolver_DefaultsOnlyWhenVaultDisabled(t *testing.T) {
	r := NewSecretResolver(&config.Config{}) // Vault.Enabled == false

	assert.False(t, r.Enabled(), "resolver must be in defaults-only mode")
	assert.Equal(t, "fallback", r.Resolve("", "fallback", "some_key"),
		"with no Vault backend Resolve returns the default")
}
