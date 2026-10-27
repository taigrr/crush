package proto

import (
	"reflect"
	"strings"
)

// Streaming wire format, version 2.
//
// Version 1 sends the whole message on every streamed token, so a turn
// costs O(n²) bytes on the wire. Version 2 sends a full message (a
// "keyframe") when a message first appears, after any change that is not
// a pure append, and periodically; in between it sends [MessageDelta]s
// that only carry appended text. Clients opt in per event stream with
// `wire=2`; servers that do not understand it keep sending version 1,
// which version 2 clients still decode.

// WireVersion is the newest event stream format this build speaks.
const WireVersion = 2

// Delta fields: the growable text field of a part.
const (
	DeltaFieldThinking = "thinking"
	DeltaFieldText     = "text"
	DeltaFieldInput    = "input"
)

// MessageDelta appends text to fields of an existing message.
type MessageDelta struct {
	ID        string `json:"id"`
	UpdatedAt int64  `json:"updated_at,omitempty"`
	// Appends are applied in order.
	Appends []PartAppend `json:"appends"`
}

// PartAppend appends Text to a part's growable field. From is the field's
// length (in bytes) before the append, so a client whose copy diverged
// can tell and wait for the next keyframe instead of corrupting text.
type PartAppend struct {
	Part  int    `json:"p"`
	Field string `json:"f"`
	From  int    `json:"from"`
	Text  string `json:"t"`
}

// growable returns the part's appendable text field, if any.
func growable(p ContentPart) (field, value string, ok bool) {
	switch v := p.(type) {
	case ReasoningContent:
		return DeltaFieldThinking, v.Thinking, true
	case TextContent:
		return DeltaFieldText, v.Text, true
	case ToolCall:
		return DeltaFieldInput, v.Input, true
	}
	return "", "", false
}

// withField returns p with its growable field replaced by value.
func withField(p ContentPart, value string) ContentPart {
	switch v := p.(type) {
	case ReasoningContent:
		v.Thinking = value
		return v
	case TextContent:
		v.Text = value
		return v
	case ToolCall:
		v.Input = value
		return v
	}
	return p
}

// DiffMessage reports whether next is prev plus pure appends to growable
// fields, returning the delta. ok is false whenever anything else changed
// (new or removed parts, a finished tool call, a signature, metadata),
// in which case the caller must send next in full.
func DiffMessage(prev, next Message) (MessageDelta, bool) {
	if prev.ID != next.ID || prev.SessionID != next.SessionID || prev.Role != next.Role ||
		prev.Model != next.Model || prev.Provider != next.Provider || prev.CreatedAt != next.CreatedAt ||
		len(prev.Parts) != len(next.Parts) {
		return MessageDelta{}, false
	}
	d := MessageDelta{ID: next.ID, UpdatedAt: next.UpdatedAt}
	for i := range next.Parts {
		pf, pv, pok := growable(prev.Parts[i])
		nf, nv, nok := growable(next.Parts[i])
		if pok != nok || pf != nf || reflect.TypeOf(prev.Parts[i]) != reflect.TypeOf(next.Parts[i]) {
			return MessageDelta{}, false
		}
		if !pok {
			if !reflect.DeepEqual(prev.Parts[i], next.Parts[i]) {
				return MessageDelta{}, false
			}
			continue
		}
		// Everything but the growable field must be unchanged.
		if !reflect.DeepEqual(withField(prev.Parts[i], ""), withField(next.Parts[i], "")) {
			return MessageDelta{}, false
		}
		if !strings.HasPrefix(nv, pv) {
			return MessageDelta{}, false
		}
		if len(nv) > len(pv) {
			d.Appends = append(d.Appends, PartAppend{Part: i, Field: nf, From: len(pv), Text: nv[len(pv):]})
		}
	}
	return d, true
}

// ApplyDelta applies d to m in place. It returns false, leaving m
// unchanged, when d does not fit m (wrong message, part, field, or base
// length).
func ApplyDelta(m *Message, d MessageDelta) bool {
	if m.ID != d.ID {
		return false
	}
	parts := append([]ContentPart(nil), m.Parts...)
	for _, a := range d.Appends {
		if a.Part < 0 || a.Part >= len(parts) {
			return false
		}
		field, value, ok := growable(parts[a.Part])
		if !ok || field != a.Field || len(value) != a.From {
			return false
		}
		parts[a.Part] = withField(parts[a.Part], value+a.Text)
	}
	m.Parts = parts
	if d.UpdatedAt != 0 {
		m.UpdatedAt = d.UpdatedAt
	}
	return true
}
