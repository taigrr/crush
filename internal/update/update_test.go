package update

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckForUpdate_Old(t *testing.T) {
	info, err := Check(t.Context(), "v0.10.0", testClient{"v0.11.0"})
	require.NoError(t, err)
	require.NotNil(t, info)
	require.True(t, info.Available())
}

func TestCheckForUpdate_Beta(t *testing.T) {
	t.Run("current is stable", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.10.0", testClient{"v0.11.0-beta.1"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.False(t, info.Available())
	})

	t.Run("current is also beta", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", testClient{"v0.11.0-beta.2"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})

	t.Run("current is beta, latest isn't", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", testClient{"v0.11.0"})
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})
}

type testClient struct{ tag string }

// Latest implements Client.
func (t testClient) Latest(ctx context.Context) (*Release, error) {
	return &Release{
		TagName: t.tag,
		HTMLURL: "https://example.org",
	}, nil
}

func TestInstallDirFor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := installDirFor(filepath.Join(dir, "crush"))
	require.NoError(t, err)
	require.Empty(t, got, "binaries under the temp dir fall back to GOBIN")

	got, err = installDirFor("/x/go-build123/b001/exe/crush")
	require.NoError(t, err)
	require.Empty(t, got)

	got, err = installDirFor("/usr/local/bin/crush-dev")
	require.NoError(t, err)
	require.Empty(t, got, "only binaries named crush are replaced")

	_, err = installDirFor("/opt/homebrew/Cellar/crush/1.0/bin/crush")
	require.ErrorContains(t, err, "Homebrew")
}
