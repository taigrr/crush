package model

import (
	"fmt"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/benchdata"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/ui/dialog"
)

const (
	benchWidth  = 200
	benchHeight = 60
)

func newBenchUI(b *testing.B, size int) *UI {
	b.Helper()
	u := newTestUI()
	u.com.Workspace = &testWorkspace{cfg: &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}}
	u.dialog = dialog.NewOverlay()
	u.keyMap = DefaultKeyMap()
	u.width, u.height = benchWidth, benchHeight
	u.setSessionMessages(benchdata.Messages("bench-session", size))
	u.updateLayoutAndSize()
	return u
}

// BenchmarkUIView measures one uncached full frame of a 200x60 chat UI with
// a long history: Draw into the canvas plus the canvas.Render() flatten,
// newline normalization and trailing-space trimming done in View.
func BenchmarkUIView(b *testing.B) {
	for _, size := range []int{500, 2000} {
		b.Run(fmt.Sprintf("msgs=%d", size), func(b *testing.B) {
			u := newBenchUI(b, size)
			u.View()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				u.View()
			}
		})
	}
}

// BenchmarkUIDraw isolates Draw into a reused screen buffer, without the
// string flattening done by View.
func BenchmarkUIDraw(b *testing.B) {
	u := newBenchUI(b, 500)
	canvas := uv.NewScreenBuffer(benchWidth, benchHeight)
	u.Draw(canvas, canvas.Bounds())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		u.Draw(canvas, canvas.Bounds())
	}
}

// BenchmarkUIScrollFrame measures a frame after scrolling the chat by one
// line, which defeats per-frame caches the way real scrolling does.
func BenchmarkUIScrollFrame(b *testing.B) {
	u := newBenchUI(b, 500)
	u.View()
	up := true
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if up {
			u.chat.ScrollBy(-1)
		} else {
			u.chat.ScrollBy(1)
		}
		up = !up
		u.View()
	}
}

// BenchmarkUISessionLoad measures turning a loaded session history into
// chat items and rendering the first frame (cold item render caches).
func BenchmarkUISessionLoad(b *testing.B) {
	for _, size := range []int{500, 2000} {
		b.Run(fmt.Sprintf("msgs=%d", size), func(b *testing.B) {
			u := newBenchUI(b, 1)
			msgs := benchdata.Messages("bench-session", size)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				u.setSessionMessages(msgs)
				u.View()
			}
		})
	}
}
