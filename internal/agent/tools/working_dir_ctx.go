package tools

import "context"

// workingDirContextKey carries the per-turn effective working directory
// from the workspace HTTP boundary (backend.runAgent) down into the
// coordinator's working-directory resolution. Like the editor bridge, the
// cwd belongs to the specific client that initiated the turn, not to the
// workspace: a single workspace (and therefore a single shared
// coordinator) can be opened by several clients at once, each launched
// from a different directory — for example sibling git worktrees that
// collapse to the same project root and thus share one workspace. Routing
// the launch cwd through the request context — mirroring
// SessionIDContextKey and WithEditorBridge — lets a shared coordinator
// resolve tools to the directory the requesting client actually launched
// from rather than whichever client created the workspace first.
type workingDirContextKey struct{}

// WithWorkingDir returns ctx tagged with the effective working directory
// for the client that initiated the current turn. Empty dirs are ignored
// so GetWorkingDirFromContext falls back to the workspace default.
func WithWorkingDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, workingDirContextKey{}, dir)
}

// GetWorkingDirFromContext returns the cwd set by WithWorkingDir, or the
// empty string when none was set. Safe to call on any context.
func GetWorkingDirFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(workingDirContextKey{}).(string); ok {
		return v
	}
	return ""
}
