package backend

import (
	"github.com/taigrr/crush/internal/hooks"
	"github.com/taigrr/crush/internal/sound"
)

// soundHookEvent maps a notification sound to the hook event that, when
// configured, owns that notification. A built-in sound defers to (stays
// silent for) its event whenever the user has configured a matching hook,
// mirroring how the end-of-turn chime defers to a Stop hook.
var soundHookEvent = map[sound.Sound]string{
	sound.EndOfTurn: hooks.EventStop,
	sound.Swarm:     hooks.EventSwarm,
	sound.Blocked:   hooks.EventBlocked,
	sound.ToolError: hooks.EventToolError,
	sound.Queued:    hooks.EventQueued,
}

// playSound plays a notification sound for the workspace. It is
// best-effort and returns immediately, delegating playback to a
// background goroutine. It is a no-op when the workspace config disables
// the sound (globally or for that event), or when the user has configured
// a matching hook — the hook then owns the notification and the built-in
// sound defers to it.
func (b *Backend) playSound(ws *Workspace, s sound.Sound) {
	if ws == nil || ws.Cfg == nil {
		return
	}
	cfg := ws.Cfg.Config()
	if cfg == nil {
		return
	}
	if !cfg.Options.SoundEnabled(s) {
		return
	}
	if event := soundHookEvent[s]; event != "" && len(cfg.Hooks[event]) > 0 {
		return
	}
	sound.PlayAsync(s, cfg.Options.SoundPath(s))
}
