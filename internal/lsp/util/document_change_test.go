package util

import (
	"os"
	"path/filepath"
	"testing"

	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestApplyDocumentChange_CreateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	err := applyDocumentChange(protocol.DocumentChange{
		CreateFile: &protocol.CreateFile{URI: protocol.URIFromPath(path)},
	}, powernap.UTF8)
	require.NoError(t, err)
	require.FileExists(t, path)
}

func TestApplyDocumentChange_CreateFileIgnoreIfExists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "exists.txt")
	require.NoError(t, os.WriteFile(path, []byte("keep me"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		CreateFile: &protocol.CreateFile{
			URI:     protocol.URIFromPath(path),
			Options: &protocol.CreateFileOptions{IgnoreIfExists: true},
		},
	}, powernap.UTF8)
	require.NoError(t, err)
	// Content must be preserved when ignoring an existing file.
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "keep me", string(got))
}

func TestApplyDocumentChange_CreateFileOverwriteTruncates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "over.txt")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		CreateFile: &protocol.CreateFile{
			URI:     protocol.URIFromPath(path),
			Options: &protocol.CreateFileOptions{Overwrite: true},
		},
	}, powernap.UTF8)
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Empty(t, string(got))
}

func TestApplyDocumentChange_DeleteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "gone.txt")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		DeleteFile: &protocol.DeleteFile{URI: protocol.URIFromPath(path)},
	}, powernap.UTF8)
	require.NoError(t, err)
	require.NoFileExists(t, path)
}

func TestApplyDocumentChange_DeleteDirRecursive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		DeleteFile: &protocol.DeleteFile{
			URI:     protocol.URIFromPath(sub),
			Options: &protocol.DeleteFileOptions{Recursive: true},
		},
	}, powernap.UTF8)
	require.NoError(t, err)
	require.NoDirExists(t, sub)
}

func TestApplyDocumentChange_DeleteNonRecursiveDirErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "f.txt"), []byte("x"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		DeleteFile: &protocol.DeleteFile{URI: protocol.URIFromPath(sub)},
	}, powernap.UTF8)
	require.Error(t, err)
}

func TestApplyDocumentChange_RenameFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	newp := filepath.Join(dir, "new.txt")
	require.NoError(t, os.WriteFile(old, []byte("data"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		RenameFile: &protocol.RenameFile{
			OldURI: protocol.URIFromPath(old),
			NewURI: protocol.URIFromPath(newp),
		},
	}, powernap.UTF8)
	require.NoError(t, err)
	require.NoFileExists(t, old)
	got, err := os.ReadFile(newp)
	require.NoError(t, err)
	require.Equal(t, "data", string(got))
}

func TestApplyDocumentChange_RenameTargetExistsNoOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	newp := filepath.Join(dir, "new.txt")
	require.NoError(t, os.WriteFile(old, []byte("data"), 0o644))
	require.NoError(t, os.WriteFile(newp, []byte("target"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		RenameFile: &protocol.RenameFile{
			OldURI:  protocol.URIFromPath(old),
			NewURI:  protocol.URIFromPath(newp),
			Options: &protocol.RenameFileOptions{Overwrite: false},
		},
	}, powernap.UTF8)
	require.Error(t, err)
	// Target must be untouched.
	got, err := os.ReadFile(newp)
	require.NoError(t, err)
	require.Equal(t, "target", string(got))
}

func TestApplyDocumentChange_RenameOverwriteAllowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	newp := filepath.Join(dir, "new.txt")
	require.NoError(t, os.WriteFile(old, []byte("data"), 0o644))
	require.NoError(t, os.WriteFile(newp, []byte("target"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		RenameFile: &protocol.RenameFile{
			OldURI:  protocol.URIFromPath(old),
			NewURI:  protocol.URIFromPath(newp),
			Options: &protocol.RenameFileOptions{Overwrite: true},
		},
	}, powernap.UTF8)
	require.NoError(t, err)
	got, err := os.ReadFile(newp)
	require.NoError(t, err)
	require.Equal(t, "data", string(got))
}

// TestApplyDocumentChange_RenameNilOptionsNoClobber verifies the
// spec-compliant default: RenameFileOptions.overwrite defaults to false, so a
// rename with nil Options must NOT overwrite an existing target.
func TestApplyDocumentChange_RenameNilOptionsNoClobber(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	old := filepath.Join(dir, "old.txt")
	newp := filepath.Join(dir, "new.txt")
	require.NoError(t, os.WriteFile(old, []byte("data"), 0o644))
	require.NoError(t, os.WriteFile(newp, []byte("target"), 0o644))

	err := applyDocumentChange(protocol.DocumentChange{
		RenameFile: &protocol.RenameFile{
			OldURI: protocol.URIFromPath(old),
			NewURI: protocol.URIFromPath(newp),
		},
	}, powernap.UTF8)
	require.Error(t, err)
	// Both files must be untouched.
	gotOld, err := os.ReadFile(old)
	require.NoError(t, err)
	require.Equal(t, "data", string(gotOld))
	gotNew, err := os.ReadFile(newp)
	require.NoError(t, err)
	require.Equal(t, "target", string(gotNew))
}
