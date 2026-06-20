// Package worktree provides management of Crush-controlled git worktrees.
// Worktrees are stored in .crush/worktrees/ and allow parallel development
// with automatic dependency management.
package worktree

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/taigrr/crush/internal/checkpoint"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/pubsub"
)

// Common errors.
var (
	ErrWorktreeNotFound  = errors.New("worktree not found")
	ErrWorktreeExists    = errors.New("worktree already exists")
	ErrInvalidName       = errors.New("invalid worktree name")
	ErrNoActiveWorktree  = errors.New("no active worktree")
	ErrWorktreesDisabled = errors.New("worktrees not enabled")
	ErrNotGitRepo        = errors.New("worktrees require the project to be a git repository")
	ErrWorktreeCreate    = errors.New("failed to create git worktree")
	ErrWorktreeMerge     = errors.New("failed to merge worktree")
	ErrDirtyWorkingTree  = errors.New("project working tree has uncommitted changes; commit or stash before merging")
)

// Service manages Crush worktrees.
type Service interface {
	pubsub.Subscriber[Worktree]

	// Create creates a new worktree, optionally from a snapshot.
	// If name is empty, generates one using conventional-commit style.
	Create(ctx context.Context, sessionID string, name string, fromSnapshotID string) (*Worktree, error)

	// Switch switches to a worktree, making it active.
	Switch(ctx context.Context, sessionID string, worktreeID string) error

	// Delete deletes a worktree and its files.
	Delete(ctx context.Context, worktreeID string) error

	// Get retrieves a worktree by ID.
	Get(ctx context.Context, worktreeID string) (*Worktree, error)

	// GetByName retrieves a worktree by session and name.
	GetByName(ctx context.Context, sessionID string, name string) (*Worktree, error)

	// GetActive returns the active worktree for a session.
	GetActive(ctx context.Context, sessionID string) (*Worktree, error)

	// GetByPath returns the managed worktree whose Path equals the
	// given filesystem path or contains it as a subdirectory. Returns
	// [ErrWorktreeNotFound] when path lies outside any managed
	// worktree (e.g. the project root itself, or an unrelated
	// directory). Path comparison is done after [filepath.Abs] +
	// [filepath.EvalSymlinks] on both sides so symlinked launches
	// still resolve correctly.
	GetByPath(ctx context.Context, path string) (*Worktree, error)

	// List lists all worktrees for a session.
	List(ctx context.Context, sessionID string) ([]*Worktree, error)

	// ListAll lists all worktrees across all sessions.
	ListAll(ctx context.Context) ([]*Worktree, error)

	// Merge merges or rebases a worktree onto a target branch.
	Merge(ctx context.Context, worktreeID, targetBranch string, rebase bool) error

	// GenerateName generates a worktree name from a description.
	GenerateName(description string) string

	// RunPostCreateHooks runs configured post-create commands.
	RunPostCreateHooks(ctx context.Context, worktreePath string) error

	// ValidateState checks for external modifications on startup.
	ValidateState(ctx context.Context) error

	// IsEnabled returns whether worktrees are enabled.
	IsEnabled() bool

	// WorktreesDir returns the directory where worktrees are stored.
	WorktreesDir() string
}

// Worktree represents a managed worktree.
type Worktree struct {
	ID             string    `json:"id"`
	SessionID      string    `json:"session_id"`
	Name           string    `json:"name"`
	Path           string    `json:"path"`
	BaseSnapshotID string    `json:"base_snapshot_id,omitempty"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
}

// service implements the Service interface.
type service struct {
	*pubsub.Broker[Worktree]

	queries     *db.Queries
	conn        *sql.DB
	checkpoints checkpoint.Service
	// projectRoot is the canonical project root — the directory whose
	// `.crush/worktrees/` we manage and whose `.git` is the main
	// repository. When Crush is launched from inside a linked
	// worktree, projectRoot is the *parent* repo, not the linked
	// worktree. All `git worktree` invocations target this root.
	projectRoot string
	worktreeDir string
	hooks       []config.PostCreateHook
	enabled     bool
}

// ServiceConfig holds configuration for the worktree service.
type ServiceConfig struct {
	// ProjectDir is the canonical project root. Worktrees are managed
	// under `<ProjectDir>/.crush/worktrees/`. Pass the main git working
	// tree root here, not a linked worktree path.
	ProjectDir      string
	Enabled         bool
	PostCreateHooks []config.PostCreateHook
}

// NewService creates a new worktree service.
func NewService(cfg ServiceConfig, queries *db.Queries, conn *sql.DB, checkpoints checkpoint.Service) (Service, error) {
	worktreeDir := filepath.Join(cfg.ProjectDir, ".crush", "worktrees")

	if cfg.Enabled {
		if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
			return nil, fmt.Errorf("create worktrees dir: %w", err)
		}
	}

	return &service{
		Broker:      pubsub.NewBroker[Worktree](),
		queries:     queries,
		conn:        conn,
		checkpoints: checkpoints,
		projectRoot: cfg.ProjectDir,
		worktreeDir: worktreeDir,
		hooks:       cfg.PostCreateHooks,
		enabled:     cfg.Enabled,
	}, nil
}

func (s *service) IsEnabled() bool {
	return s.enabled
}

func (s *service) WorktreesDir() string {
	return s.worktreeDir
}

func (s *service) Create(ctx context.Context, sessionID string, name string, fromSnapshotID string) (*Worktree, error) {
	if !s.enabled {
		return nil, ErrWorktreesDisabled
	}

	// Generate name if not provided.
	if name == "" {
		name = s.GenerateName("")
	}

	// Validate name.
	if !isValidWorktreeName(name) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidName, name)
	}

	// Check if worktree already exists.
	_, err := s.GetByName(ctx, sessionID, name)
	if err == nil {
		return nil, ErrWorktreeExists
	}
	if !errors.Is(err, ErrWorktreeNotFound) {
		return nil, err
	}

	// Require a git repository: worktrees are real linked git worktrees
	// of the user's project, not filesystem copies.
	if !s.isGitRepo(ctx) {
		return nil, ErrNotGitRepo
	}

	worktreePath := filepath.Join(s.worktreeDir, name)

	// git worktree add creates the directory itself and refuses to run
	// if it already exists, so make sure it is clear first.
	if _, err := os.Stat(worktreePath); err == nil {
		return nil, ErrWorktreeExists
	}

	// Create a real linked worktree on a new branch named after the
	// worktree, based on the current HEAD. This shares the project's
	// object store (no duplication) and never copies files by hand.
	if out, err := s.git(ctx, s.projectRoot, "worktree", "add", "-b", name, worktreePath, "HEAD"); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWorktreeCreate, strings.TrimSpace(out))
	}

	// Deactivate other worktrees for this session.
	if err := s.queries.DeactivateSessionWorktrees(ctx, sessionID); err != nil {
		slog.Debug("Failed to deactivate session worktrees", "error", err)
	}

	// Create database record.
	id := uuid.New().String()
	now := time.Now()

	worktree := &Worktree{
		ID:             id,
		SessionID:      sessionID,
		Name:           name,
		Path:           worktreePath,
		BaseSnapshotID: fromSnapshotID,
		IsActive:       true,
		CreatedAt:      now,
	}

	if err := s.queries.CreateWorktree(ctx, db.CreateWorktreeParams{
		ID:             id,
		SessionID:      sessionID,
		Name:           name,
		Path:           worktreePath,
		BaseSnapshotID: toNullString(fromSnapshotID),
		IsActive:       1,
		CreatedAt:      now.UnixMilli(),
	}); err != nil {
		// Roll back the git worktree we just created so we don't leave
		// an orphan on disk with no DB record.
		s.removeGitWorktree(ctx, worktreePath)
		return nil, fmt.Errorf("create worktree record: %w", err)
	}

	// Run post-create hooks.
	if err := s.RunPostCreateHooks(ctx, worktreePath); err != nil {
		slog.Debug("Post-create hooks failed", "error", err)
	}

	s.Publish(pubsub.CreatedEvent, *worktree)

	return worktree, nil
}

func (s *service) Switch(ctx context.Context, sessionID string, worktreeID string) error {
	if !s.enabled {
		return ErrWorktreesDisabled
	}

	worktree, err := s.Get(ctx, worktreeID)
	if err != nil {
		return err
	}

	// Verify worktree belongs to session.
	if worktree.SessionID != sessionID {
		return ErrWorktreeNotFound
	}

	// Deactivate all worktrees for session.
	if err := s.queries.DeactivateSessionWorktrees(ctx, sessionID); err != nil {
		return fmt.Errorf("deactivate worktrees: %w", err)
	}

	// Activate this worktree.
	if err := s.queries.SetWorktreeActive(ctx, db.SetWorktreeActiveParams{
		IsActive: 1,
		ID:       worktreeID,
	}); err != nil {
		return fmt.Errorf("activate worktree: %w", err)
	}

	worktree.IsActive = true
	s.Publish(pubsub.UpdatedEvent, *worktree)

	return nil
}

func (s *service) Delete(ctx context.Context, worktreeID string) error {
	if !s.enabled {
		return ErrWorktreesDisabled
	}

	worktree, err := s.Get(ctx, worktreeID)
	if err != nil {
		return err
	}

	// Remove the linked git worktree (deletes the directory and the
	// repo's admin entry). Falls back to a plain directory removal if
	// git can't manage it (e.g. a legacy copy-based worktree).
	s.removeGitWorktree(ctx, worktree.Path)

	// Delete database record.
	if err := s.queries.DeleteWorktree(ctx, worktreeID); err != nil {
		return fmt.Errorf("delete worktree record: %w", err)
	}

	s.Publish(pubsub.DeletedEvent, *worktree)

	return nil
}

func (s *service) Get(ctx context.Context, worktreeID string) (*Worktree, error) {
	if !s.enabled {
		return nil, ErrWorktreesDisabled
	}

	row, err := s.queries.GetWorktree(ctx, worktreeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorktreeNotFound
		}
		return nil, err
	}

	return dbRowToWorktree(row), nil
}

func (s *service) GetByName(ctx context.Context, sessionID string, name string) (*Worktree, error) {
	if !s.enabled {
		return nil, ErrWorktreesDisabled
	}

	row, err := s.queries.GetWorktreeByName(ctx, db.GetWorktreeByNameParams{
		SessionID: sessionID,
		Name:      name,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrWorktreeNotFound
		}
		return nil, err
	}

	return dbRowToWorktree(row), nil
}

func (s *service) GetActive(ctx context.Context, sessionID string) (*Worktree, error) {
	if !s.enabled {
		return nil, ErrWorktreesDisabled
	}

	row, err := s.queries.GetActiveWorktree(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoActiveWorktree
		}
		return nil, err
	}

	return dbRowToWorktree(row), nil
}

func (s *service) GetByPath(ctx context.Context, path string) (*Worktree, error) {
	if !s.enabled {
		return nil, ErrWorktreesDisabled
	}
	if path == "" {
		return nil, ErrWorktreeNotFound
	}

	resolved, err := canonicalizePath(path)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	all, err := s.ListAll(ctx)
	if err != nil {
		return nil, err
	}

	// Prefer the longest match so nested worktrees (if a user ever
	// creates `feat/a` and `feat/a/sub`) resolve to the deepest one
	// containing the cwd. Equal-length matches are unique by
	// construction (Path is unique in the DB).
	var best *Worktree
	bestLen := 0
	for _, wt := range all {
		wtPath, err := canonicalizePath(wt.Path)
		if err != nil {
			continue
		}
		if !pathContainsOrEqual(wtPath, resolved) {
			continue
		}
		if len(wtPath) > bestLen {
			best = wt
			bestLen = len(wtPath)
		}
	}
	if best == nil {
		return nil, ErrWorktreeNotFound
	}
	return best, nil
}

func (s *service) List(ctx context.Context, sessionID string) ([]*Worktree, error) {
	if !s.enabled {
		return nil, nil
	}

	// If no sessionID provided, list all worktrees.
	if sessionID == "" {
		return s.ListAll(ctx)
	}

	rows, err := s.queries.ListWorktrees(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	worktrees := make([]*Worktree, len(rows))
	for i, row := range rows {
		worktrees[i] = dbRowToWorktree(row)
	}

	return worktrees, nil
}

func (s *service) ListAll(ctx context.Context) ([]*Worktree, error) {
	if !s.enabled {
		return nil, nil
	}

	rows, err := s.queries.ListAllWorktrees(ctx)
	if err != nil {
		return nil, err
	}

	worktrees := make([]*Worktree, len(rows))
	for i, row := range rows {
		worktrees[i] = dbRowToWorktree(row)
	}

	return worktrees, nil
}

func (s *service) Merge(ctx context.Context, worktreeID, targetBranch string, rebase bool) error {
	if !s.enabled {
		return ErrWorktreesDisabled
	}

	wt, err := s.Get(ctx, worktreeID)
	if err != nil {
		return err
	}
	wtBranch := wt.Name

	// Never merge over uncommitted work in the user's main checkout:
	// the merge below moves the main repo's HEAD, which would clobber a
	// dirty tree. Fail loudly instead.
	if dirty, err := s.isWorkingTreeDirty(ctx); err != nil {
		return err
	} else if dirty {
		return ErrDirtyWorkingTree
	}

	// Remember the branch the user was on so we can restore it.
	originalBranch, err := s.currentBranch(ctx)
	if err != nil {
		return err
	}

	if rebase {
		// Rebase the feature branch onto target inside its own
		// worktree, so we never check out wtBranch in the main repo
		// (git forbids the same branch in two worktrees anyway).
		if out, err := s.git(ctx, wt.Path, "rebase", targetBranch); err != nil {
			return fmt.Errorf("%w: rebase: %s", ErrWorktreeMerge, strings.TrimSpace(out))
		}
	}

	if out, err := s.git(ctx, s.projectRoot, "checkout", targetBranch); err != nil {
		return fmt.Errorf("%w: checkout target: %s", ErrWorktreeMerge, strings.TrimSpace(out))
	}

	mergeArgs := []string{"merge", "--no-edit", wtBranch}
	if rebase {
		// After a rebase the feature branch is ahead of target, so a
		// fast-forward is both possible and the cleanest result.
		mergeArgs = []string{"merge", "--ff-only", wtBranch}
	}
	if out, err := s.git(ctx, s.projectRoot, mergeArgs...); err != nil {
		// Best-effort restore so a failed merge doesn't strand the user
		// on the target branch.
		if originalBranch != "" && originalBranch != targetBranch {
			_, _ = s.git(ctx, s.projectRoot, "checkout", originalBranch)
		}
		return fmt.Errorf("%w: %s", ErrWorktreeMerge, strings.TrimSpace(out))
	}

	// Restore the user's original branch so the merge is transparent.
	if originalBranch != "" && originalBranch != targetBranch {
		if out, err := s.git(ctx, s.projectRoot, "checkout", originalBranch); err != nil {
			return fmt.Errorf("%w: restore branch: %s", ErrWorktreeMerge, strings.TrimSpace(out))
		}
	}

	return nil
}

// git runs a git subcommand in dir and returns combined output.
func (s *service) git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// isGitRepo reports whether the project directory is inside a git work tree.
func (s *service) isGitRepo(ctx context.Context) bool {
	out, err := s.git(ctx, s.projectRoot, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// isWorkingTreeDirty reports whether the project's main checkout has
// uncommitted changes (staged, unstaged, or untracked).
func (s *service) isWorkingTreeDirty(ctx context.Context) (bool, error) {
	out, err := s.git(ctx, s.projectRoot, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("%w: status: %s", ErrWorktreeMerge, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out) != "", nil
}

// currentBranch returns the project's currently checked-out branch name,
// or "" if detached/unknown.
func (s *service) currentBranch(ctx context.Context) (string, error) {
	out, err := s.git(ctx, s.projectRoot, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("%w: current branch: %s", ErrWorktreeMerge, strings.TrimSpace(out))
	}
	branch := strings.TrimSpace(out)
	if branch == "HEAD" { // detached
		return "", nil
	}
	return branch, nil
}

// removeGitWorktree removes a linked worktree via git, pruning admin
// state. If git can't manage the path (e.g. a legacy copy-based
// worktree), it falls back to removing the directory directly. All
// failures are best-effort and logged at debug level.
func (s *service) removeGitWorktree(ctx context.Context, path string) {
	if out, err := s.git(ctx, s.projectRoot, "worktree", "remove", "--force", path); err != nil {
		slog.Debug("Git worktree remove failed, falling back to rm", "path", path, "output", strings.TrimSpace(out), "error", err)
		if rmErr := os.RemoveAll(path); rmErr != nil {
			slog.Debug("Failed to remove worktree directory", "path", path, "error", rmErr)
		}
		_, _ = s.git(ctx, s.projectRoot, "worktree", "prune")
	}
}

// GenerateName generates a worktree name from a description.
// Uses conventional-commit style: feat/add-something, fix/issue-123
func (s *service) GenerateName(description string) string {
	if description == "" {
		// Generate a timestamp-based name.
		return fmt.Sprintf("worktree-%d", time.Now().Unix())
	}

	// Clean and convert to slug.
	name := strings.ToLower(description)
	name = regexp.MustCompile(`[^a-z0-9\s-]`).ReplaceAllString(name, "")
	name = regexp.MustCompile(`\s+`).ReplaceAllString(name, "-")
	name = regexp.MustCompile(`-+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")

	// Limit length.
	if len(name) > 40 {
		name = name[:40]
		name = strings.TrimSuffix(name, "-")
	}

	if name == "" {
		name = fmt.Sprintf("worktree-%d", time.Now().Unix())
	}

	return name
}

func (s *service) RunPostCreateHooks(ctx context.Context, worktreePath string) error {
	if len(s.hooks) == 0 {
		return nil
	}

	var lastErr error
	for _, hook := range s.hooks {
		// Check if the trigger file exists.
		triggerPath := filepath.Join(worktreePath, hook.IfExists)
		if _, err := os.Stat(triggerPath); os.IsNotExist(err) {
			continue
		}

		// Run the command.
		slog.Debug("Running post-create hook", "command", hook.Run, "trigger", hook.IfExists)

		cmd := exec.CommandContext(ctx, "sh", "-c", hook.Run)
		cmd.Dir = worktreePath
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			slog.Warn("Post-create hook failed", "command", hook.Run, "error", err)
			lastErr = err
		}
	}

	return lastErr
}

func (s *service) ValidateState(ctx context.Context) error {
	if !s.enabled {
		return nil
	}

	// Get all worktrees from database.
	dbWorktrees, err := s.ListAll(ctx)
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}

	dbPaths := make(map[string]*Worktree)
	for _, wt := range dbWorktrees {
		dbPaths[wt.Path] = wt
	}

	// Scan filesystem for worktrees.
	entries, err := os.ReadDir(s.worktreeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read worktrees dir: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		fsPath := filepath.Join(s.worktreeDir, entry.Name())

		if _, inDB := dbPaths[fsPath]; !inDB {
			slog.Warn("Orphan worktree found (exists on disk but not in database)",
				"path", fsPath,
				"name", entry.Name())
		}
	}

	// Check for stale DB records.
	for path, wt := range dbPaths {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			slog.Warn("Stale worktree record (exists in database but not on disk)",
				"id", wt.ID,
				"name", wt.Name,
				"path", path)
		}
	}

	return nil
}

func dbRowToWorktree(row any) *Worktree {
	switch r := row.(type) {
	case db.Worktree:
		return &Worktree{
			ID:             r.ID,
			SessionID:      r.SessionID,
			Name:           r.Name,
			Path:           r.Path,
			BaseSnapshotID: fromNullString(r.BaseSnapshotID),
			IsActive:       r.IsActive != 0,
			CreatedAt:      time.UnixMilli(r.CreatedAt),
		}
	case *db.Worktree:
		return &Worktree{
			ID:             r.ID,
			SessionID:      r.SessionID,
			Name:           r.Name,
			Path:           r.Path,
			BaseSnapshotID: fromNullString(r.BaseSnapshotID),
			IsActive:       r.IsActive != 0,
			CreatedAt:      time.UnixMilli(r.CreatedAt),
		}
	default:
		return nil
	}
}

func toNullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func fromNullString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// isValidWorktreeName checks if a worktree name is valid.
func isValidWorktreeName(name string) bool {
	if name == "" {
		return false
	}
	if len(name) > 100 {
		return false
	}
	// Only allow alphanumeric, hyphens, underscores, and forward slashes (for feat/xxx style).
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9][a-zA-Z0-9/_-]*$`, name)
	return matched
}

// canonicalizePath returns an absolute, symlink-resolved form of path.
// EvalSymlinks failures (e.g. the path no longer exists) fall back to
// the absolute form so a stale DB row pointing at a deleted worktree
// still produces a stable comparable key.
func canonicalizePath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}
	return abs, nil
}

// pathContainsOrEqual reports whether parent equals child, or child
// is a descendant of parent. Both inputs must already be cleaned
// absolute paths; on Windows comparisons are case-insensitive.
func pathContainsOrEqual(parent, child string) bool {
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	// `..` (or starting with `..`) means child escapes parent.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
