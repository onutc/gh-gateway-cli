package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestActiveTokenFromTokenCommandEnv(t *testing.T) {
	authCfg := newTestAuthConfig(t)
	t.Setenv("GH_TOKEN_COMMAND", "printf 'token-from-command\\n'")

	token, source := authCfg.ActiveToken("github.com")

	require.Equal(t, "token-from-command", token)
	require.Equal(t, "GH_TOKEN_COMMAND", source)
}

func TestHasEnvTokenWithTokenCommandEnv(t *testing.T) {
	authCfg := newTestAuthConfig(t)
	t.Setenv("GH_TOKEN_COMMAND", "printf 'token-from-command\\n'")

	require.True(t, authCfg.HasEnvToken())
}
