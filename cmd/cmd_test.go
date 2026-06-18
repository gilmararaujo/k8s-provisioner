package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/techiescamp/k8s-provisioner/internal/config"
)

func TestVaultClientFromConfig_NilConfigErrors(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg = nil

	_, err := vaultClientFromConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "config not loaded")
}

func TestVaultClientFromConfig_BuildsClientWhenTokenPresent(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg = &config.Config{}
	cfg.Vault.Token = "hvs.test"
	cfg.Vault.Addr = "http://192.168.56.20:8200"

	c, err := vaultClientFromConfig()

	require.NoError(t, err)
	assert.NotNil(t, c)
}

func TestUserCommands_RequireExactlyOneArg(t *testing.T) {
	for _, c := range []*cobra.Command{userCreateCmd, userDeleteCmd, userCreateRoleCmd} {
		require.Error(t, c.Args(c, []string{}), "%s: zero args must fail", c.Name())
		require.Error(t, c.Args(c, []string{"a", "b"}), "%s: two args must fail", c.Name())
		require.NoError(t, c.Args(c, []string{"alice"}), "%s: one arg must pass", c.Name())
	}
}

func TestVaultGetCmd_AcceptsOneOrTwoArgs(t *testing.T) {
	require.Error(t, vaultGetCmd.Args(vaultGetCmd, []string{}))
	require.NoError(t, vaultGetCmd.Args(vaultGetCmd, []string{"path"}))
	require.NoError(t, vaultGetCmd.Args(vaultGetCmd, []string{"path", "key"}))
	require.Error(t, vaultGetCmd.Args(vaultGetCmd, []string{"a", "b", "c"}))
}

func TestVaultSetCmd_RequiresThreeArgs(t *testing.T) {
	require.Error(t, vaultSetCmd.Args(vaultSetCmd, []string{"p", "k"}))
	require.NoError(t, vaultSetCmd.Args(vaultSetCmd, []string{"p", "k", "v"}))
}

func TestConfigGetters_ReturnPackageState(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg = &config.Config{}

	assert.Same(t, cfg, GetConfig())
}
