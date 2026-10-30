package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// The realm script runs in bash, and generated secrets may contain $ and #:
// every credential must reach kcadm.sh verbatim, not shell-expanded.
func TestConfigureRealm_PassesCredentialsVerbatim(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	sh := &fakeShell{outputs: map[string]string{"kubectl get pods": "keycloak-0"}}
	creds := keycloakCreds{
		adminUsername:     "admin",
		adminPassword:     "a$HOME#1",
		grafanaSecret:     "g$PATH#x!@2",
		k8sAdminPassword:  "k$1#@!",
		developerPassword: "d$$#3",
	}
	require.NoError(t, NewKeycloak(&config.Config{}, sh).configureRealm("", creds))
	require.Len(t, sh.stdins, 1)

	// Swap kcadm.sh for a stub that logs one argument per line.
	dir := t.TempDir()
	argLog, stub := filepath.Join(dir, "args"), filepath.Join(dir, "kcadm.sh")
	require.NoError(t, os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+argLog+"\n"), 0o700)) // #nosec G306 -- test stub
	script := strings.Replace(sh.stdins[0], "KCADM=/opt/keycloak/bin/kcadm.sh", "KCADM="+stub, 1)
	out, err := exec.Command("bash", "-c", script).CombinedOutput() // #nosec G204 -- rendered test script
	require.NoError(t, err, string(out))

	logged, err := os.ReadFile(argLog) // #nosec G304 -- temp file
	require.NoError(t, err)
	args := strings.Split(string(logged), "\n")
	assert.Contains(t, args, creds.adminPassword)
	assert.Contains(t, args, "secret="+creds.grafanaSecret)
	assert.Contains(t, args, creds.k8sAdminPassword)
	assert.Contains(t, args, creds.developerPassword)
}
