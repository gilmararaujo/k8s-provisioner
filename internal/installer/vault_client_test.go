package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveVaultToken_PrefersConfigToken(t *testing.T) {
	// A token from config short-circuits the vault-init.json file read.
	assert.Equal(t, "hvs.abc123", ResolveVaultToken("hvs.abc123"))
}
