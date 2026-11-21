package checkpoint

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func benchWorkTree(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("node_modules\n*.log\n"), 0o644)
	for i := range 40 {
		d := filepath.Join(dir, "pkg"+strconv.Itoa(i), "sub")
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "f.go"), []byte("package x"), 0o644)
		if i%4 == 0 {
			_ = os.WriteFile(filepath.Join(dir, "pkg"+strconv.Itoa(i), ".gitignore"), []byte("tmp/\n"), 0o644)
		}
	}
	_ = os.MkdirAll(filepath.Join(dir, "node_modules", "a"), 0o755)
	return dir
}

// BenchmarkCheckpointReopenFirstSnapshot measures reopening the checkpoint
// repo for an already-seen work tree and taking the first snapshot, which
// is what each repeated TUI attach does.
func BenchmarkCheckpointReopenFirstSnapshot(b *testing.B) {
	dir := benchWorkTree(b)
	cfg := DefaultConfig()
	if _, err := InitRepoAt(dir, dir, cfg); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		r, err := InitRepoAt(dir, dir, cfg)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := r.CreateSnapshot("bench"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCheckpointReopen(b *testing.B) {
	dir := benchWorkTree(b)
	cfg := DefaultConfig()
	if _, err := InitRepoAt(dir, dir, cfg); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := InitRepoAt(dir, dir, cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkIgnoreCacheCycle300 reads ignore patterns round-robin across 300
// small work trees, as a server hosting hundreds of workspaces does.
func BenchmarkIgnoreCacheCycle300(b *testing.B) {
	root := b.TempDir()
	dirs := make([]string, 300)
	for i := range dirs {
		d := filepath.Join(root, "ws"+strconv.Itoa(i))
		for j := range 8 {
			_ = os.MkdirAll(filepath.Join(d, "p"+strconv.Itoa(j)), 0o755)
		}
		_ = os.WriteFile(filepath.Join(d, ".gitignore"), []byte("*.log\n"), 0o644)
		dirs[i] = d
	}
	ignoreCache = ignoreCacheStore{}
	for _, d := range dirs {
		_, _ = readIgnorePatterns(d)
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if _, err := readIgnorePatterns(dirs[i%len(dirs)]); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
