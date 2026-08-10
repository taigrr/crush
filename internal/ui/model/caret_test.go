package model

import (
	"testing"

	"github.com/taigrr/crush/internal/ui/common"
)

// TestEditorCaret_TracksEditorOrigin verifies the reported caret is the
// textarea-local cursor translated by the editor's origin. Opening the left
// session navigator shifts the editor right, and the caret must shift with it
// instead of staying pinned to the app margin.
func TestEditorCaret_TracksEditorOrigin(t *testing.T) {
	t.Parallel()

	newUI := func(sidebar bool) *UI {
		u := newTestUI()
		u.leftSidebar = NewSessionsSidebar(common.DefaultCommon(nil))
		u.leftSidebarVisible = sidebar
		u.updateLayoutAndSize()
		return u
	}

	check := func(t *testing.T, u *UI, label string) int {
		t.Helper()
		local := u.textarea.Cursor()
		if local == nil {
			t.Fatalf("%s: expected a textarea cursor", label)
		}
		cur := u.editorCaret()
		if cur == nil {
			t.Fatalf("%s: expected a caret with the editor focused", label)
		}
		if got, want := cur.X, u.layout.editor.Min.X+local.X; got != want {
			t.Errorf("%s: caret X = %d, want editor origin + local cursor = %d", label, got, want)
		}
		return cur.X
	}

	without := newUI(false)
	with := newUI(true)

	xWithout := check(t, without, "without navigator")
	xWith := check(t, with, "with navigator")

	// Guard against the assertions above passing trivially: the navigator
	// must actually shift the editor, and the caret must follow it by exactly
	// the navigator's width plus its one-column gap.
	if with.layout.editor.Min.X <= without.layout.editor.Min.X {
		t.Fatalf("expected the navigator to shift the editor right: %d -> %d",
			without.layout.editor.Min.X, with.layout.editor.Min.X)
	}
	if delta, want := xWith-xWithout, defaultLeftSidebarWidth+1; delta != want {
		t.Errorf("caret shift = %d, want navigator width plus gap = %d", delta, want)
	}
}

// TestEditorCaret_HiddenWhenEditorCollapsed verifies no caret is reported when
// the editor has no visible rows.
func TestEditorCaret_HiddenWhenEditorCollapsed(t *testing.T) {
	t.Parallel()

	u := newTestUI()
	u.updateLayoutAndSize()
	u.layout.editor.Max.Y = u.layout.editor.Min.Y

	if got := u.editorCaret(); got != nil {
		t.Errorf("expected no caret for a collapsed editor, got %+v", got)
	}
}

// TestResizeLeftSidebar_ChangesLayoutAndClamps verifies a resize moves the
// editor by the same amount as the navigator grows, and that the width stops
// at the supported bounds.
func TestResizeLeftSidebar_ChangesLayoutAndClamps(t *testing.T) {
	t.Parallel()

	newUI := func() *UI {
		u := newTestUI()
		u.leftSidebar = NewSessionsSidebar(common.DefaultCommon(nil))
		u.leftSidebarVisible = true
		u.focus = uiFocusLeftSidebar
		u.updateLayoutAndSize()
		return u
	}

	t.Run("widening shifts the editor right by the same amount", func(t *testing.T) {
		t.Parallel()

		u := newUI()
		beforeSidebar := u.layout.leftSidebar.Dx()
		beforeEditor := u.layout.editor.Min.X

		// Drop the persist command; only the local geometry matters here.
		_ = u.resizeLeftSidebar(leftSidebarResizeStep)

		if got, want := u.leftSidebarWidth, beforeSidebar+leftSidebarResizeStep; got != want {
			t.Fatalf("width = %d, want %d", got, want)
		}
		if got, want := u.layout.leftSidebar.Dx(), beforeSidebar+leftSidebarResizeStep; got != want {
			t.Errorf("navigator rect = %d, want %d", got, want)
		}
		if got, want := u.layout.editor.Min.X, beforeEditor+leftSidebarResizeStep; got != want {
			t.Errorf("editor origin = %d, want %d", got, want)
		}
		// The caret must stay glued to the editor across a resize.
		cur := u.editorCaret()
		local := u.textarea.Cursor()
		if cur == nil || local == nil {
			t.Fatal("expected a caret and a textarea cursor")
		}
		if got, want := cur.X, u.layout.editor.Min.X+local.X; got != want {
			t.Errorf("caret X after resize = %d, want %d", got, want)
		}
	})

	t.Run("narrowing stops at the minimum", func(t *testing.T) {
		t.Parallel()

		u := newUI()
		for range 100 {
			_ = u.resizeLeftSidebar(-leftSidebarResizeStep)
		}
		if got := u.leftSidebarWidth; got != minLeftSidebarWidth {
			t.Errorf("width = %d, want min %d", got, minLeftSidebarWidth)
		}
	})

	t.Run("widening stops at the maximum or the terminal's room", func(t *testing.T) {
		t.Parallel()

		u := newUI()
		for range 100 {
			_ = u.resizeLeftSidebar(leftSidebarResizeStep)
		}
		ceiling := min(maxLeftSidebarWidth, u.width-12)
		if got := u.leftSidebarWidth; got > ceiling {
			t.Errorf("width = %d, want <= %d", got, ceiling)
		}
		// The main pane must survive the widest navigator.
		if u.layout.main.Dx() <= 0 {
			t.Errorf("main pane collapsed: %d", u.layout.main.Dx())
		}
	})

	t.Run("a no-op resize at the bound returns no command", func(t *testing.T) {
		t.Parallel()

		u := newUI()
		for range 100 {
			_ = u.resizeLeftSidebar(-leftSidebarResizeStep)
		}
		if cmd := u.resizeLeftSidebar(-leftSidebarResizeStep); cmd != nil {
			t.Error("expected no persist command when the width does not change")
		}
	})
}
