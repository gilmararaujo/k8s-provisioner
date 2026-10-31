package provisioner

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

// recordingExecutor records shell commands and runs nothing.
type recordingExecutor struct{ cmds []string }

func (r *recordingExecutor) RunShell(cmd string) (string, error) {
	r.cmds = append(r.cmds, cmd)
	return "", nil
}
func (r *recordingExecutor) RunShellWithOutput(cmd string) error {
	_, err := r.RunShell(cmd)
	return err
}
func (r *recordingExecutor) RunShellWithStdin(cmd, _ string) (string, error) {
	return r.RunShell(cmd)
}
func (r *recordingExecutor) Run(string, ...string) (string, error) { return "", nil }
func (r *recordingExecutor) RunWithOutput(string, ...string) error { return nil }

func TestSetTimezone(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cluster.Timezone = "America/Sao_Paulo"
	rec := &recordingExecutor{}

	require.NoError(t, (&Provisioner{config: cfg, exec: rec}).setTimezone())
	assert.Equal(t, []string{"timedatectl set-timezone America/Sao_Paulo"}, rec.cmds)

	rec.cmds = nil
	require.NoError(t, (&Provisioner{config: &config.Config{}, exec: rec}).setTimezone())
	assert.Equal(t, []string{"timedatectl set-timezone UTC"}, rec.cmds, "unset means UTC")
}
