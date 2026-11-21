package model

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/benchdata"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/ui/dialog"
)

// TestViewCanvasReuseMatchesFresh checks that a View drawn into the reused
// canvas is byte-identical to one drawn into a freshly allocated canvas,
// across scrolls and resizes.
func TestViewCanvasReuseMatchesFresh(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.com.Workspace = &testWorkspace{cfg: &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}}
	u.dialog = dialog.NewOverlay()
	u.keyMap = DefaultKeyMap()
	u.frames = nil
	u.width, u.height = 200, 60
	u.setSessionMessages(benchdata.Messages("canvas-reuse", 50))
	u.updateLayoutAndSize()

	fresh := func() string {
		u.canvas = uv.ScreenBuffer{}
		return u.View().Content
	}
	steps := []func(){
		func() {},
		func() { u.chat.ScrollBy(-3) },
		func() { u.chat.ScrollBy(-10) },
		func() { u.width, u.height = 140, 45; u.updateLayoutAndSize() },
		func() { u.chat.ScrollBy(5) },
		func() { u.width, u.height = 160, 50; u.updateLayoutAndSize() },
		func() { u.chat.ScrollBy(-7) },
	}
	u.View()
	for i, step := range steps {
		step()
		reused := u.View().Content
		prev := u.canvas
		want := fresh()
		require.Equal(t, want, reused, "step %d", i)
		u.canvas = prev
		require.Equal(t, want, u.View().Content, "step %d repeat", i)
	}
}
