package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// permissions.sysadmin is parsed and a project config overrides the global.
func TestConfig_PermissionsSysadmin(t *testing.T) {
	t.Parallel()

	t.Run("absent defaults to false", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadFromBytes([][]byte{[]byte(`{"permissions": {"allowed_tools": ["view"]}}`)})
		require.NoError(t, err)
		require.NotNil(t, cfg.Permissions)
		require.False(t, cfg.Permissions.Sysadmin)
	})

	t.Run("global true is honored", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadFromBytes([][]byte{[]byte(`{"permissions": {"sysadmin": true}}`)})
		require.NoError(t, err)
		require.NotNil(t, cfg.Permissions)
		require.True(t, cfg.Permissions.Sysadmin)
	})

	t.Run("later config overrides earlier", func(t *testing.T) {
		t.Parallel()
		cfg, err := loadFromBytes([][]byte{
			[]byte(`{"permissions": {"sysadmin": true}}`),
			[]byte(`{"permissions": {"sysadmin": false}}`),
		})
		require.NoError(t, err)
		require.NotNil(t, cfg.Permissions)
		require.False(t, cfg.Permissions.Sysadmin)
	})
}
