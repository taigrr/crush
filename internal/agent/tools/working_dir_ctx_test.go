package tools

import (
	"context"
	"testing"
)

func TestWorkingDirContext(t *testing.T) {
	t.Parallel()

	t.Run("round trips a non-empty dir", func(t *testing.T) {
		t.Parallel()
		ctx := WithWorkingDir(context.Background(), "/proj/wt")
		if got := GetWorkingDirFromContext(ctx); got != "/proj/wt" {
			t.Fatalf("GetWorkingDirFromContext() = %q, want %q", got, "/proj/wt")
		}
	})

	t.Run("empty dir is ignored", func(t *testing.T) {
		t.Parallel()
		ctx := WithWorkingDir(context.Background(), "")
		if got := GetWorkingDirFromContext(ctx); got != "" {
			t.Fatalf("GetWorkingDirFromContext() = %q, want empty", got)
		}
	})

	t.Run("absent key yields empty", func(t *testing.T) {
		t.Parallel()
		if got := GetWorkingDirFromContext(context.Background()); got != "" {
			t.Fatalf("GetWorkingDirFromContext() = %q, want empty", got)
		}
	})

	t.Run("later value overrides earlier", func(t *testing.T) {
		t.Parallel()
		ctx := WithWorkingDir(context.Background(), "/a")
		ctx = WithWorkingDir(ctx, "/b")
		if got := GetWorkingDirFromContext(ctx); got != "/b" {
			t.Fatalf("GetWorkingDirFromContext() = %q, want %q", got, "/b")
		}
	})
}
