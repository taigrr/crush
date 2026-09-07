package anim

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// TestLowBandwidthRender exercises the reduced-motion render path.
// Table-driven: each step in the cycle should produce a deterministic
// "Label N-dots" output with no gradient/cycling chars.
func TestLowBandwidthRender(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		label     string
		ticks     int
		wantPlain string
	}{
		{
			name:      "step 0 shows one dot",
			label:     "Generating",
			ticks:     0,
			wantPlain: "Generating .",
		},
		{
			name:      "step 1 shows two dots",
			label:     "Generating",
			ticks:     1,
			wantPlain: "Generating ..",
		},
		{
			name:      "step 2 shows three dots",
			label:     "Generating",
			ticks:     2,
			wantPlain: "Generating ...",
		},
		{
			name:      "wraps back to one dot after three",
			label:     "Generating",
			ticks:     3,
			wantPlain: "Generating .",
		},
		{
			name:      "no label still renders dots",
			label:     "",
			ticks:     0,
			wantPlain: ".",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := New(Settings{Label: tt.label, LowBandwidth: true})
			for range tt.ticks {
				advanceLowBandwidthFrame(a)
			}
			require.Equal(t, tt.wantPlain, ansi.Strip(a.Render()))
		})
	}
}

// TestLowBandwidthSkipsHeavyState verifies that the constructor does not
// allocate the gradient/cycling-frames machinery in low-bandwidth mode.
// This guards against regressions where someone re-introduces the
// expensive prerender path.
func TestLowBandwidthSkipsHeavyState(t *testing.T) {
	t.Parallel()
	a := New(Settings{Label: "Generating", LowBandwidth: true})
	require.True(t, a.lowBandwidth)
	require.Nil(t, a.cyclingFrames, "cyclingFrames should not be populated")
	require.Nil(t, a.initialFrames, "initialFrames should not be populated")
}

// TestSetDefaultLowBandwidth proves the package-level fallback wires
// through New() when Settings doesn't set LowBandwidth explicitly.
func TestSetDefaultLowBandwidth(t *testing.T) {
	// Cannot use t.Parallel: mutates package state.
	t.Cleanup(func() { SetDefaultLowBandwidth(false) })

	SetDefaultLowBandwidth(true)
	a := New(Settings{Label: "Generating"})
	require.True(t, a.lowBandwidth, "expected default flag to be inherited")

	SetDefaultLowBandwidth(false)
	b := New(Settings{Label: "Generating"})
	require.False(t, b.lowBandwidth, "expected default flag to be cleared")
}

// TestLowBandwidthLabelStaysVisible guards against the spinner visually
// disappearing between cycles \u2014 we explicitly chose "., .., ..." rather
// than the standard "., .., ..., (empty)" so the user always has a sign
// the agent is alive.
func TestLowBandwidthLabelStaysVisible(t *testing.T) {
	t.Parallel()
	a := New(Settings{Label: "Generating", LowBandwidth: true})
	for i := range 12 {
		plain := ansi.Strip(a.Render())
		require.True(t, strings.Contains(plain, "."), "tick %d had no dot: %q", i, plain)
		advanceLowBandwidthFrame(a)
	}
}

// advanceLowBandwidthFrame feeds a per-instance low-bandwidth Anim enough
// fast-clock frames to advance one dot frame while the process-wide flag
// is off, mirroring what the shared UI clock delivers.
func advanceLowBandwidthFrame(a *Anim) {
	for range lowBandwidthDivider {
		a.Advance()
	}
}

// TestLowBandwidthInstanceDividesFastClock covers the per-instance case:
// an Anim built in low-bandwidth mode while the process-wide flag is off
// is driven by the 20 Hz shared clock and must only change its dot frame
// once per lowBandwidthFrameInterval, not on every tick.
func TestLowBandwidthInstanceDividesFastClock(t *testing.T) {
	t.Parallel()
	a := New(Settings{Label: "Generating", LowBandwidth: true})
	require.Equal(t, "Generating .", ansi.Strip(a.Render()))
	for range lowBandwidthDivider - 1 {
		a.Advance()
		require.Equal(t, "Generating .", ansi.Strip(a.Render()), "dot frame must hold across fast-clock frames")
	}
	a.Advance()
	require.Equal(t, "Generating ..", ansi.Strip(a.Render()))
}

// TestLowBandwidthGlobalUsesSlowClock covers the process-wide case: when
// the flag is on the shared clock already ticks at the slow interval and
// every Advance must be a dot frame.
func TestLowBandwidthGlobalUsesSlowClock(t *testing.T) {
	// Cannot t.Parallel: mutates package state.
	t.Cleanup(func() { SetDefaultLowBandwidth(false) })
	SetDefaultLowBandwidth(true)
	require.Equal(t, lowBandwidthFrameInterval, FrameInterval())

	a := New(Settings{Label: "Generating"})
	require.Equal(t, "Generating .", ansi.Strip(a.Render()))
	a.Advance()
	require.Equal(t, "Generating ..", ansi.Strip(a.Render()))

	SetDefaultLowBandwidth(false)
	require.Equal(t, time.Second/fps, FrameInterval())
}

// TestLowBandwidthLiveToggleDownshiftsExistingAnim is the regression
// test for the user-reported bug: toggling the package-wide flag must
// downshift a *normal-mode* Anim that was constructed before the
// toggle. Without it, in-flight assistant/tool spinners keep showing
// the gradient scrambler until the next message item is created.
func TestLowBandwidthLiveToggleDownshiftsExistingAnim(t *testing.T) {
	// Cannot t.Parallel: mutates package state.
	t.Cleanup(func() { SetDefaultLowBandwidth(false) })

	a := New(Settings{Label: "Generating"})
	require.False(t, a.lowBandwidth, "constructed in normal mode")

	// Normal-mode render contains gradient cycling chars; nothing about
	// the visible output should match the calm-dots renderer yet.
	plainBefore := ansi.Strip(a.Render())
	require.NotEqual(t, "Generating .", plainBefore)
	require.NotEqual(t, "Generating ..", plainBefore)
	require.NotEqual(t, "Generating ...", plainBefore)

	// Toggle the package flag mid-flight (palette toggle path).
	SetDefaultLowBandwidth(true)

	plainAfter := ansi.Strip(a.Render())
	require.Contains(t, plainAfter, "Generating", "label preserved after toggle")
	require.Contains(t, plainAfter, ".", "dots renderer engaged after toggle")
	// Width of the calm renderer is bounded; the scrambler's 15-char
	// cycling area is gone.
	require.LessOrEqual(t, len(plainAfter), len("Generating ..."),
		"after toggle, render should be short calm form, got %q", plainAfter)
}
