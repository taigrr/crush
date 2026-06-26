package checkpoint

import (
	"io/fs"
	"testing"

	"github.com/go-git/go-git/v5/plumbing/filemode"
)

func TestTreeFileMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		mode fs.FileMode
		want filemode.FileMode
	}{
		{"regular file", 0o644, filemode.Regular},
		{"executable owner bit", 0o744, filemode.Executable},
		{"executable group bit", 0o654, filemode.Executable},
		{"executable other bit", 0o645, filemode.Executable},
		{"plain symlink", fs.ModeSymlink | 0o777, filemode.Symlink},
		{"symlink takes precedence over exec bits", fs.ModeSymlink | 0o755, filemode.Symlink},
		{"zero perms is regular", 0, filemode.Regular},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := treeFileMode(tc.mode); got != tc.want {
				t.Fatalf("treeFileMode(%v) = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}
