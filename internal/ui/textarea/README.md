# textarea

A fork of `charm.land/bubbles/v2/textarea` (v2.2.1, MIT — see LICENSE)
carried in-tree because Crush needs editor features upstream does not
expose:

- **Marks** (`marks.go`): buffer positions that follow the text through
  edits, with left/right gravity. Every public mutation entry point
  (`Update`, `SetValue`, `InsertString`, `InsertRune`, `DeleteSelection`,
  `ReplaceRange`) adjusts them, so callers can track a span without
  searching the buffer for it.
- **Highlights** (`highlight.go`): styled ranges delimited by marks,
  layered under the selection in the renderer.
- **Range editing** (`edit.go`): `ReplaceRange`, `TextIn`, `SetCursor`,
  `CursorPosition` — local edits that preserve viewport, selection, and
  cursor.

Voice dictation (`internal/ui/model/voice_dictation.go`) is the consumer.
Everything else is unmodified upstream; keep diffs against upstream
confined to the files above plus the `segmentSpans`/`renderSegment` hook
in `textarea.go`'s view loop.
