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

func TestGeneratePassword_Unique(t *testing.T) {
	a, err := generatePassword(32)
	require.NoError(t, err)
	b, err := generatePassword(32)
	require.NoError(t, err)

	require.NotEqual(t, a, b, "two generated passwords should differ")
}
