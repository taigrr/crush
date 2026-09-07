package model

import (
	"maps"
	"slices"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"

	"github.com/taigrr/crush/internal/ui/textarea"
	"github.com/taigrr/crush/internal/voice"
)

// dictationEditor is what the dictation state machine needs from the
// prompt editor. *textarea.Model satisfies it.
type dictationEditor interface {
	CursorPosition() textarea.Position
	TextIn(start, end textarea.Position) string
	ReplaceRange(start, end textarea.Position, s string) textarea.Position
	AddMark(pos textarea.Position, g textarea.Gravity) textarea.MarkID
	SetMark(id textarea.MarkID, pos textarea.Position)
	Mark(id textarea.MarkID) (textarea.Position, bool)
	RemoveMark(id textarea.MarkID)
	SetHighlights(hs ...textarea.Highlight)
}

type dictationState int

const (
	dictationIdle dictationState = iota
	dictationListening
	dictationStopping
)

// phrase is a turn's in-progress transcript inside the editor, delimited
// by marks so it follows the text through the user's own edits.
type phrase struct {
	start, end textarea.MarkID
}

// dictation maps pipeline events onto edits of the prompt. It owns all
// per-turn bookkeeping: which turns are live, each live turn's phrase,
// and whether the current turn is recording, draining, or done.
//
// A turn's events are applied only while it is live (pressed and not yet
// reported Stopped/Error), so anything from a turn that was reset away is
// ignored. Only the current turn drives the recording state; a
// predecessor still draining its final just settles its phrase.
type dictation struct {
	ed    dictationEditor
	style lipgloss.Style

	turn      int
	state     dictationState
	holdOwned bool
	live      map[int]bool
	phrases   map[int]phrase
}

func newDictation(ed dictationEditor, style lipgloss.Style) *dictation {
	return &dictation{
		ed:      ed,
		style:   style,
		live:    map[int]bool{},
		phrases: map[int]phrase{},
	}
}

func (d *dictation) listening() bool { return d.state == dictationListening }

func (d *dictation) active() bool { return d.state != dictationIdle }

// holdOwnedNow reports whether the chord is being held for the current
// (recording or draining) turn.
func (d *dictation) holdOwnedNow() bool {
	return d.holdOwned && d.active()
}

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

// reset forgets every turn. Text already in the editor keeps its normal
// styling.
func (d *dictation) reset() {
	d.state = dictationIdle
	d.holdOwned = false
	clear(d.live)
	d.dropPhrases()
}

// commit releases every phrase to normal styling without waiting for
// finals; used when the prompt is consumed.
func (d *dictation) commit() {
	d.dropPhrases()
}

func (d *dictation) dropPhrases() {
	for _, p := range d.phrases {
		d.ed.RemoveMark(p.start)
		d.ed.RemoveMark(p.end)
	}
	clear(d.phrases)
	d.ed.SetHighlights()
}

// apply handles one pipeline event. It returns an error event to surface
// to the user when the current turn failed; the session must then be
// torn down by the caller.
func (d *dictation) apply(ev voice.Event) (failure *voice.Event) {
	if !d.live[ev.Turn] {
		return nil
	}
	current := ev.Turn == d.turn
	switch ev.Kind {
	case voice.EventInterim:
		if current && d.listening() {
			d.setPhrase(ev.Turn, ev.Text)
		}
	case voice.EventFinal:
		d.setPhrase(ev.Turn, ev.Text)
		d.releasePhrase(ev.Turn)
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
	d.releasePhrase(turn)
	if turn == d.turn && d.active() {
		d.state = dictationIdle
		d.holdOwned = false
	}
}

// setPhrase replaces turn's phrase text in the editor, or inserts it when
// the turn has none yet. The editor's mark tracking keeps the phrase's
// range and the caret correct through the edit: a caret sitting at the
// end of the phrase follows it, a caret elsewhere stays on its character.
func (d *dictation) setPhrase(turn int, text string) {
	text = strings.Join(strings.Fields(text), " ")
	p, ok := d.phrases[turn]
	var start, end textarea.Position
	if ok {
		start, _ = d.ed.Mark(p.start)
		end, _ = d.ed.Mark(p.end)
	} else {
		if text == "" {
			return
		}
		start = d.anchorFor(turn)
		end = start
	}
	padded := padVoiceText(d.charBefore(start), text, d.charAfter(end))
	insEnd := d.ed.ReplaceRange(start, end, padded)
	if ok {
		// The phrase knows exactly where it is; pin the marks rather than
		// relying on gravity, which matters when the user had collapsed
		// the range to nothing.
		d.ed.SetMark(p.start, start)
		d.ed.SetMark(p.end, insEnd)
	} else {
		d.phrases[turn] = phrase{
			start: d.ed.AddMark(start, textarea.GravityRight),
			end:   d.ed.AddMark(insEnd, textarea.GravityLeft),
		}
	}
	d.refreshHighlights()
}

// releasePhrase forgets turn's phrase, leaving its text in place with
// normal styling.
func (d *dictation) releasePhrase(turn int) {
	p, ok := d.phrases[turn]
	if !ok {
		return
	}
	d.ed.RemoveMark(p.start)
	d.ed.RemoveMark(p.end)
	delete(d.phrases, turn)
	d.refreshHighlights()
}

// anchorFor is where a turn's first text goes: at the caret, except that
// a turn which never produced an interim (released before any partial)
// lands before the next newer turn's phrase so spoken order is kept.
func (d *dictation) anchorFor(turn int) textarea.Position {
	for _, id := range slices.Sorted(maps.Keys(d.phrases)) {
		if id > turn {
			if pos, ok := d.ed.Mark(d.phrases[id].start); ok {
				return pos
			}
		}
	}
	return d.ed.CursorPosition()
}

func (d *dictation) refreshHighlights() {
	hs := make([]textarea.Highlight, 0, len(d.phrases))
	for _, id := range slices.Sorted(maps.Keys(d.phrases)) {
		p := d.phrases[id]
		hs = append(hs, textarea.Highlight{Start: p.start, End: p.end, Style: d.style})
	}
	d.ed.SetHighlights(hs...)
}

func (d *dictation) charBefore(pos textarea.Position) string {
	if pos.Col == 0 {
		if pos.Row == 0 {
			return ""
		}
		return "\n"
	}
	return d.ed.TextIn(textarea.Position{Row: pos.Row, Col: pos.Col - 1}, pos)
}

func (d *dictation) charAfter(pos textarea.Position) string {
	return d.ed.TextIn(pos, textarea.Position{Row: pos.Row, Col: pos.Col + 1})
}

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
