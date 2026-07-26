package filepathext

import (
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Windows uses USERPROFILE for os.UserHomeDir.
	t.Setenv("USERPROFILE", home)

	require.Equal(t, home, ExpandTilde("~"))
	require.Equal(t, filepath.Join(home, "foo", "bar"), ExpandTilde("~/foo/bar"))
	require.Equal(t, "notilde", ExpandTilde("notilde"))
	require.Equal(t, "/abs/path", ExpandTilde("/abs/path"))

	// A real user resolves to their home dir. Resolve the expected value the
	// same way ExpandTilde does (user.Lookup) so this holds regardless of cgo
	// build mode.
	if cur, err := user.Current(); err == nil {
		if u, err := user.Lookup(cur.Username); err == nil && u.HomeDir != "" {
			require.Equal(t, u.HomeDir, ExpandTilde("~"+u.Username))
			require.Equal(t, filepath.Join(u.HomeDir, "foo"), ExpandTilde("~"+u.Username+"/foo"))
		}
	}
	// An unknown user is left untouched.
	require.Equal(t, "~definitelynotarealuser12345/foo", ExpandTilde("~definitelynotarealuser12345/foo"))
}

func TestSmartJoinExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	require.Equal(t, filepath.Join(home, "foo"), SmartJoin("/some/wd", "~/foo"))
	require.Equal(t, filepath.Join("/some/wd", "rel"), SmartJoin("/some/wd", "rel"))
}
