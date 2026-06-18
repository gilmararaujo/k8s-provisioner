package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSecret_ExistingVaultValueWins(t *testing.T) {
	updates := map[string]string{}

	got := resolveSecret(map[string]string{"k": "fromVault"}, updates, "k", "generated")

	require.Equal(t, "fromVault", got)
	assert.Empty(t, updates, "an existing value must not be staged for write-back")
}

func TestResolveSecret_GeneratedStagedWhenAbsent(t *testing.T) {
	updates := map[string]string{}

	got := resolveSecret(nil, updates, "k2", "generated") // existing may be nil

	require.Equal(t, "generated", got)
	assert.Equal(t, "generated", updates["k2"], "generated value must be staged")
}

func TestResolveSecret_EmptyExistingTreatedAsAbsent(t *testing.T) {
	updates := map[string]string{}

	got := resolveSecret(map[string]string{"k": ""}, updates, "k", "generated")

	require.Equal(t, "generated", got)
	assert.Equal(t, "generated", updates["k"])
}
