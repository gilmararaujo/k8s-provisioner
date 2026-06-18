package executor

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These cover the real subprocess paths (sh -c) on Unix hosts.
func skipOnWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh -c based tests are Unix-only")
	}
}

func TestRunShell_CapturesStdout(t *testing.T) {
	skipOnWindows(t)

	out, err := New(false).RunShell("printf hello")

	require.NoError(t, err)
	assert.Equal(t, "hello", strings.TrimSpace(out))
}

func TestRunShell_NonZeroExitReturnsError(t *testing.T) {
	skipOnWindows(t)

	_, err := New(false).RunShell("exit 3")

	require.Error(t, err)
}

func TestRunShellWithStdin_PipesStdinToCommand(t *testing.T) {
	skipOnWindows(t)

	out, err := New(false).RunShellWithStdin("cat", "piped-data")

	require.NoError(t, err)
	assert.Equal(t, "piped-data", strings.TrimSpace(out))
}

func TestRun_ArgvFormCapturesStdout(t *testing.T) {
	skipOnWindows(t)

	out, err := New(false).Run("printf", "argv-hello")

	require.NoError(t, err)
	assert.Equal(t, "argv-hello", strings.TrimSpace(out))
}

func TestRun_NonZeroExitReturnsError(t *testing.T) {
	skipOnWindows(t)

	_, err := New(false).Run("false")

	require.Error(t, err)
}
