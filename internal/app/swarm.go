package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
	"github.com/taigrr/crush/internal/ui/styles"
)

// SwarmConfig returns the swarm identity generator config resolved
// from the active theme (if any). It is safe to call from any
// goroutine: the theme is re-read from disk on each call so config
// reloads take effect without restart. Errors reading the theme file
// silently fall back to the built-in defaults.
func (app *App) SwarmConfig() swarm.Config {
	cfg := app.config.Config()
	themesDir := config.GlobalThemesDir()
	name := ""
	if cfg != nil && cfg.Options != nil && cfg.Options.TUI != nil {
		name = cfg.Options.TUI.Theme
	}
	theme := styles.ResolveSwarmTheme(name, themesDir)
	return swarm.Config{
		Palette: theme.Palette,
		Animals: theme.Animals,
	}
}

// SwarmEnabled reports whether the cross-session swarm feature is
// turned on. On by default.
func (app *App) SwarmEnabled() bool {
	cfg := app.config.Config()
	if cfg == nil || cfg.Options == nil {
		return true
	}
	return cfg.Options.SwarmEnabled()
}

// backfillSwarmIdentities assigns color/animal to any session that is
// missing one. Idempotent — sessions that already have both fields
// are skipped. Sub-sessions (title, summary, task tool children) are
// included so they can be looked up by address if needed, but the
// swarm tool refuses to send to them.
func (app *App) backfillSwarmIdentities(ctx context.Context) error {
	sessions, err := app.Sessions.List(ctx)
	if err != nil {
		return err
	}
	archived, err := app.Sessions.ListArchived(ctx)
	if err != nil {
		// Non-fatal: still process visible sessions, but log so a
		// permission or schema issue on the archived table is
		// auditable rather than invisible.
		slog.Warn("Failed to list archived sessions during swarm backfill", "error", err)
		archived = nil
	}
	// Copy into a fresh slice so we never mutate the caller's list
	// slice header even if it has spare capacity.
	all := make([]session.Session, 0, len(sessions)+len(archived))
	all = append(all, sessions...)
	all = append(all, archived...)
	cfg := app.SwarmConfig()
	var errs []error
	for _, s := range all {
		if s.Color != "" && s.Animal != "" {
			continue
		}
		id := swarm.Assign(s.ID, cfg)
		if err := app.Sessions.SetSwarmIdentity(ctx, s.ID, id.Color, id.Animal); err != nil {
			errs = append(errs, err)
			slog.Warn("Failed to backfill swarm identity",
				"session_id", s.ID, "error", err)
		}
	}
	return errors.Join(errs...)
}

// EnsureSwarmIdentity guarantees the given session has an assigned
// (color, animal) pair, computing and persisting one on demand. This
// is the single choke point for both freshly-created sessions and
// legacy rows that missed the startup backfill.
func (app *App) EnsureSwarmIdentity(ctx context.Context, s session.Session) (session.Session, error) {
	if s.Color != "" && s.Animal != "" {
		return s, nil
	}
	cfg := app.SwarmConfig()
	id := swarm.Assign(s.ID, cfg)
	if err := app.Sessions.SetSwarmIdentity(ctx, s.ID, id.Color, id.Animal); err != nil {
		return s, err
	}
	s.Color = id.Color
	s.Animal = id.Animal
	return s, nil
}

// assignSwarmIdentityOnCreate consumes session-service events from
// the pre-subscribed channel and stamps a color/animal on any new
// top-level session as soon as it appears. Sub-sessions
// (title/summary/task children) are deliberately skipped: they are
// never addressable and inherit their parent's context.
//
// Exits when either ctx is done (app shutdown) or the pubsub channel
// is closed. Under normal shutdown the broker cancels the
// subscription on ctx.Done and closes the channel; the extra
// ctx.Done branch is a belt-and-suspenders check that guarantees
// exit even if the broker races.
func (app *App) assignSwarmIdentityOnCreate(ctx context.Context, ch <-chan pubsub.Event[session.Session]) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Type != pubsub.CreatedEvent {
				continue
			}
			s := ev.Payload
			if s.ParentSessionID != "" {
				continue
			}
			if s.Color != "" && s.Animal != "" {
				continue
			}
			if _, err := app.EnsureSwarmIdentity(context.Background(), s); err != nil {
				slog.Debug("Failed to assign swarm identity on create",
					"session_id", s.ID, "error", err)
			}
		}
	}
}
