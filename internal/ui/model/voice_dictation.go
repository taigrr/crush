package model

import (
	"maps"
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	"github.com/taigrr/crush/internal/voice"
)

type dictationState int

const (
	dictationIdle dictationState = iota
	dictationListening
	dictationStopping
)

// phrase is a turn's in-progress transcript inside the prompt, as flat
// rune offsets into the buffer (rows joined by '\n').
type phrase struct {
	start, end int
}

// dictation maps pipeline events onto edits of the prompt. It owns all
// per-turn bookkeeping: which turns are live, each live turn's phrase,
// and whether the current turn is recording, draining, or done.
//
// A turn's events are applied only while it is live (pressed and not yet
// reported Stopped/Error), so anything from a turn that was reset away is
// ignored. Only the current turn drives the recording state; a
// predecessor still draining its final just settles its phrase.
//
// Phrases follow the text through the user's own edits: before every
// event and every render the buffer is diffed against the last snapshot
// and phrase offsets are shifted through the changed region, so the
// textarea itself needs no hooks.
type dictation struct {
	ta    *textarea.Model
	style lipgloss.Style

	turn      int
	state     dictationState
	holdOwned bool
	live      map[int]bool
	phrases   map[int]phrase
	// last is the buffer as of the most recent reconcile.
	last []rune
}

func newDictation(ta *textarea.Model, style lipgloss.Style) *dictation {
	return &dictation{
		ta:      ta,
		style:   style,
		live:    map[int]bool{},
		phrases: map[int]phrase{},
	}
}

func (d *dictation) listening() bool { return d.state == dictationListening }

func (d *dictation) active() bool { return d.state != dictationIdle }

// holdOwnedNow reports whether the chord is being held for the current
// (recording or draining) turn.
func (d *dictation) holdOwnedNow() bool { return d.holdOwned && d.active() }

// pending reports whether any turn still has events outstanding.
func (d *dictation) pending() bool {
	return d.active() || len(d.live) > 0 || len(d.phrases) > 0
}

// begin starts a new turn and returns its id.
func (d *dictation) begin(fromHold bool) int {
	d.turn++
	d.live[d.turn] = true
	d.state = dictationListening
	d.holdOwned = fromHold
	return d.turn
}

// release ends the current turn's recording; its final is still awaited.
func (d *dictation) release() {
	if d.state == dictationListening {
		d.state = dictationStopping
	}
	d.holdOwned = false
}

// reset forgets every turn. Text already in the prompt keeps its normal
// styling.
func (d *dictation) reset() {
	d.state = dictationIdle
	d.holdOwned = false
	clear(d.live)
	clear(d.phrases)
}

// commit releases every phrase to normal styling without waiting for
// finals; used when the prompt is consumed.
func (d *dictation) commit() { clear(d.phrases) }

// apply handles one pipeline event. It returns the event when the current
// turn failed and the caller must tear the session down.
func (d *dictation) apply(ev voice.Event) (failure *voice.Event) {
	if !d.live[ev.Turn] {
		return nil
	}
	d.sync()
	current := ev.Turn == d.turn
	switch ev.Kind {
	case voice.EventInterim:
		if current && d.listening() {
			d.setPhrase(ev.Turn, ev.Text)
		}
	case voice.EventFinal:
		d.setPhrase(ev.Turn, ev.Text)
		delete(d.phrases, ev.Turn)
		if current && d.state == dictationStopping {
			d.finish(ev.Turn)
		}
	case voice.EventStopped:
		d.finish(ev.Turn)
	case voice.EventError:
		d.finish(ev.Turn)
		if current {
			d.reset()
			return &ev
		}
	}
	return nil
}

// finish marks a turn over. Only the current turn changes the state.
func (d *dictation) finish(turn int) {
	delete(d.live, turn)
	delete(d.phrases, turn)
	if turn == d.turn && d.active() {
		d.state = dictationIdle
		d.holdOwned = false
	}
}

// sync reconciles phrase offsets with edits made to the buffer since the
// last call, by mapping them through the single replaced region found by
// a common prefix/suffix diff. Phrase starts have right gravity (typing
// at the start stays outside the phrase) and ends left gravity.
func (d *dictation) sync() {
	cur := []rune(d.ta.Value())
	if slices.Equal(cur, d.last) {
		return
	}
	before := d.last
	prefix := 0
	for prefix < len(before) && prefix < len(cur) && before[prefix] == cur[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(before)-prefix && suffix < len(cur)-prefix &&
		before[len(before)-1-suffix] == cur[len(cur)-1-suffix] {
		suffix++
	}
	d.shiftPhrases(prefix, len(before)-suffix, len(cur)-suffix, -1)
	d.last = cur
}

// shiftPhrases maps every phrase except skip through the replacement of
// flat range [start, oldEnd) by [start, newEnd).
func (d *dictation) shiftPhrases(start, oldEnd, newEnd, skip int) {
	for id, p := range d.phrases {
		if id == skip {
			continue
		}
		p.start = shiftOffset(p.start, start, oldEnd, newEnd, true)
		p.end = shiftOffset(p.end, start, oldEnd, newEnd, false)
		if p.end < p.start {
			p.end = p.start
		}
		d.phrases[id] = p
	}
}

// shiftOffset maps one offset through an edit. Offsets strictly inside
// the replaced range collapse to its start; for a pure insertion at the
// offset, right gravity moves it past the inserted text.
func shiftOffset(p, start, oldEnd, newEnd int, rightGravity bool) int {
	switch {
	case p < start:
		return p
	case p == start:
		if start == oldEnd && rightGravity {
			return newEnd
		}
		return p
	case p >= oldEnd:
		return p + newEnd - oldEnd
	default:
		return start
	}
}

// setPhrase replaces turn's phrase text in the prompt, or inserts it when
// the turn has none yet. The caret follows the phrase when it sat at the
// phrase's end; otherwise it stays on the same character.
func (d *dictation) setPhrase(turn int, text string) {
	text = strings.Join(strings.Fields(text), " ")
	cur := d.last
	p, ok := d.phrases[turn]
	if !ok {
		if text == "" {
			return
		}
		p.start = d.anchorFor(turn)
		p.end = p.start
	}
	padded := []rune(padVoiceText(string(cur[:p.start]), text, string(cur[p.end:])))
	insEnd := p.start + len(padded)

	caret := d.caretOffset()
	switch {
	case caret < p.start:
	case caret == p.start && p.start != p.end:
	case caret <= p.end:
		caret = insEnd
	default:
		caret += insEnd - p.end
	}

	next := make([]rune, 0, len(cur)-(p.end-p.start)+len(padded))
	next = append(append(append(next, cur[:p.start]...), padded...), cur[p.end:]...)
	d.ta.SetValue(string(next))
	d.shiftPhrases(p.start, p.end, insEnd, turn)
	d.phrases[turn] = phrase{start: p.start, end: insEnd}
	d.last = []rune(d.ta.Value())
	d.setCaret(caret)
}

// anchorFor is where a turn's first text goes: at the caret, except that
// a turn which never produced an interim (released before any partial)
// lands before the next newer turn's phrase so spoken order is kept.
func (d *dictation) anchorFor(turn int) int {
	for _, id := range slices.Sorted(maps.Keys(d.phrases)) {
		if id > turn {
			return d.phrases[id].start
		}
	}
	return d.caretOffset()
}

// view renders the prompt with the current turn's in-progress phrase in
// the dictation style. The highlight is drawn on a copy of the textarea
// through its selection machinery, so wrapping and scrolling stay native;
// a user selection takes precedence and disables the highlight.
func (d *dictation) view() string {
	d.sync()
	p, ok := d.phrases[d.turn]
	if !ok {
		for _, id := range slices.Sorted(maps.Keys(d.phrases)) {
			p, ok = d.phrases[id], true
			break
		}
	}
	if !ok || p.start == p.end || d.ta.HasSelection() || !d.ta.Focused() {
		return d.ta.View()
	}
	copyTA := *d.ta
	styles := copyTA.Styles()
	styles.Focused.Selection = d.style
	styles.Blurred.Selection = d.style
	copyTA.SetStyles(styles)
	sx, sy := screenCoords(&copyTA, d.positionOf(p.start))
	ex, ey := screenCoords(&copyTA, d.positionOf(p.end))
	copyTA.BeginSelection(sx, sy)
	copyTA.ExtendSelection(ex, ey)
	copyTA.EndSelection()
	return copyTA.View()
}

// screenCoords inverts [textarea.Model.PositionAt]: the textarea-relative
// cell at which pos is drawn. Positions scrolled out of view clamp to the
// first or last visible cell, which is what a highlight spanning the
// viewport edge needs.
func screenCoords(ta *textarea.Model, pos textarea.Position) (x, y int) {
	xMax := ta.Width() + screenCoordGutter
	height := ta.Height()
	if posBefore(pos, ta.PositionAt(0, 0)) {
		return 0, 0
	}
	for y = range height {
		first, last := ta.PositionAt(0, y), ta.PositionAt(xMax, y)
		if posBefore(pos, first) || posBefore(last, pos) {
			continue
		}
		lo, hi := 0, xMax
		for lo < hi {
			mid := (lo + hi) / 2
			if posBefore(ta.PositionAt(mid, y), pos) {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		return lo, y
	}
	return xMax, height - 1
}

// screenCoordGutter over-estimates the prompt/line-number gutter so the x
// scan covers the whole row; PositionAt clamps columns past the text.
const screenCoordGutter = 8

func posBefore(p, q textarea.Position) bool {
	if p.Row != q.Row {
		return p.Row < q.Row
	}
	return p.Col < q.Col
}

func (d *dictation) caretOffset() int {
	return d.offsetOf(textarea.Position{Row: d.ta.Line(), Col: d.ta.Column()})
}

// offsetOf converts a buffer position to a flat offset into d.last.
func (d *dictation) offsetOf(pos textarea.Position) int {
	start, row := 0, 0
	for i, r := range d.last {
		if row == pos.Row {
			break
		}
		if r == '\n' {
			row++
			start = i + 1
		}
	}
	if row < pos.Row {
		return len(d.last)
	}
	return min(start+pos.Col, start+lineLen(d.last[start:]))
}

func lineLen(rs []rune) int {
	for i, r := range rs {
		if r == '\n' {
			return i
		}
	}
	return len(rs)
}

// positionOf converts a flat offset into d.last to a buffer position.
func (d *dictation) positionOf(off int) textarea.Position {
	off = max(0, min(off, len(d.last)))
	pos := textarea.Position{}
	for _, r := range d.last[:off] {
		if r == '\n' {
			pos.Row++
			pos.Col = 0
		} else {
			pos.Col++
		}
	}
	return pos
}

// setCaret moves the real caret to a flat offset using the public cursor
// API and scrolls it into view.
func (d *dictation) setCaret(off int) {
	pos := d.positionOf(off)
	d.ta.MoveToBegin()
	for steps := 0; d.ta.Line() < pos.Row && steps < maxCursorSteps; steps++ {
		before := d.ta.Line()
		d.ta.CursorDown()
		if d.ta.Line() == before {
			break
		}
	}
	d.ta.SetCursorColumn(pos.Col)
	// SetCursorColumn does not scroll; SetHeight with the current height
	// is the public way to bring the caret's visual line into view.
	d.ta.SetHeight(d.ta.Height())
}

// maxCursorSteps bounds the visual-line walk in setCaret.
const maxCursorSteps = 100_000

// padVoiceText adds a leading space when the insertion point follows a
// non-space rune and a trailing space when it precedes one, so speech
// never fuses with neighbouring words.
func padVoiceText(before, text, after string) string {
	if text == "" {
		return ""
	}
	if r, ok := lastRune(before); ok && !unicode.IsSpace(r) {
		text = " " + text
	}
	if r, ok := firstRune(after); ok && !unicode.IsSpace(r) {
		text += " "
	}
	return text
}

func lastRune(s string) (rune, bool) {
	if s == "" {
		return 0, false
	}
	rs := []rune(s)
	return rs[len(rs)-1], true
}

func firstRune(s string) (rune, bool) {
	for _, r := range s {
		return r, true
	}
	return 0, false
}
