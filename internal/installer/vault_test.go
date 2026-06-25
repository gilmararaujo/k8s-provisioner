package installer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const passwordCharset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$"

func TestGeneratePassword_LengthAndCharset(t *testing.T) {
	const n = 24

	pw, err := generatePassword(n)

	require.NoError(t, err)
	require.Len(t, pw, n)
	for _, c := range pw {
		require.Contains(t, passwordCharset, string(c), "char %q not in allowed charset", string(c))
	}
}

// Generated passwords are interpolated into bash scripts (e.g. keycloak_realm.go
// single-quotes them as '%s'). Quoting only holds if the value can never contain
// a single quote or backtick, so guard the charset against those metacharacters.
func TestGeneratePassword_NoShellMetacharacters(t *testing.T) {
	for i := 0; i < 1000; i++ {
		pw, err := generatePassword(32)
		require.NoError(t, err)
		require.NotContains(t, pw, "'", "single quote would break bash single-quoting")
		require.NotContains(t, pw, "`", "backtick allows command substitution")
		require.NotContains(t, pw, "\\", "backslash can escape the closing quote")
	}
}

func TestGeneratePassword_Unique(t *testing.T) {
	a, err := generatePassword(32)
	require.NoError(t, err)
	b, err := generatePassword(32)
	require.NoError(t, err)

	require.NotEqual(t, a, b, "two generated passwords should differ")
}
