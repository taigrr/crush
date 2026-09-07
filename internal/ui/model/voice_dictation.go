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

type phrase struct {
	start, end int
}

// Events are applied only for live turns (pressed, not yet Stopped), so a
// turn reset away is ignored while a predecessor still draining its final
// settles its phrase. Phrase offsets survive the user's own edits by
// diffing the buffer against the last snapshot before each event/render.
type dictation struct {
	ta    *textarea.Model
	style lipgloss.Style

	turn      int
	state     dictationState
	holdOwned bool
	live      map[int]bool
	phrases   map[int]phrase
	last      []rune
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

func (d *dictation) holdOwnedNow() bool { return d.holdOwned && d.active() }

func (d *dictation) pending() bool {
	return d.active() || len(d.live) > 0 || len(d.phrases) > 0
}

func (d *dictation) begin(fromHold bool) int {
	d.turn++
	d.live[d.turn] = true
	d.state = dictationListening
	d.holdOwned = fromHold
	return d.turn
}

func (d *dictation) release() {
	if d.state == dictationListening {
		d.state = dictationStopping
	}
	d.holdOwned = false
}

func (d *dictation) reset() {
	d.state = dictationIdle
	d.holdOwned = false
	clear(d.live)
	clear(d.phrases)
}

func (d *dictation) commit() { clear(d.phrases) }

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

func (d *dictation) finish(turn int) {
	delete(d.live, turn)
	delete(d.phrases, turn)
	if turn == d.turn && d.active() {
		d.state = dictationIdle
		d.holdOwned = false
	}
}

// Phrase starts have right gravity (typing at the start stays outside the
// phrase) and ends left gravity.
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

// A turn released before any partial lands before the next newer turn's
// phrase so spoken order is kept.
func (d *dictation) anchorFor(turn int) int {
	for _, id := range slices.Sorted(maps.Keys(d.phrases)) {
		if id > turn {
			return d.phrases[id].start
		}
	}
	return d.caretOffset()
}

// The highlight is drawn on a copy of the textarea through its selection
// machinery so wrapping and scrolling stay native.
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

// screenCoords inverts PositionAt; positions scrolled out of view clamp to
// the first/last visible cell.
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

const maxCursorSteps = 100_000

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
