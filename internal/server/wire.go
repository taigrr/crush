package server

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
)

// keyframeEvery forces a full message after this many deltas, so a client
// that ever diverged resynchronizes on its own.
const keyframeEvery = 1000

// maxFrame caps the coalescing interval a client may request.
const maxFrame = 500 * time.Millisecond

// streamOptions are negotiated per event stream via query parameters.
type streamOptions struct {
	// wire is the format version the client accepts (1 or 2).
	wire int
	// frame, when non-zero, coalesces streamed message updates and sends
	// at most one per message per frame.
	frame time.Duration
}

func parseStreamOptions(q map[string][]string) streamOptions {
	get := func(k string) string {
		if v := q[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	o := streamOptions{wire: 1}
	if v, err := strconv.Atoi(get("wire")); err == nil && v >= 2 {
		o.wire = min(v, proto.WireVersion)
	}
	if ms, err := strconv.Atoi(get("frame_ms")); err == nil && ms > 0 {
		o.frame = min(time.Duration(ms)*time.Millisecond, maxFrame)
	}
	return o
}

// streamEncoder turns backend events into wire payloads for one client
// stream, replacing streamed message updates with deltas (wire 2).
type streamEncoder struct {
	opts   streamOptions
	last   map[string]proto.Message
	deltas map[string]int
	// pending holds the newest coalesced update per message, in arrival
	// order, until the next frame flush.
	pending      map[string]proto.Message
	pendingOrder []string
}

func newStreamEncoder(opts streamOptions) *streamEncoder {
	return &streamEncoder{
		opts:    opts,
		last:    map[string]proto.Message{},
		deltas:  map[string]int{},
		pending: map[string]proto.Message{},
	}
}

// isStreamingUpdate reports whether ev is a message update eligible for
// coalescing and deltas.
func isStreamingUpdate(ev any) (message.Message, bool) {
	e, ok := ev.(pubsub.Event[message.Message])
	return e.Payload, ok && e.Type == pubsub.UpdatedEvent
}

// add consumes one backend event and returns the payloads to send now.
// With framing enabled, message updates are held for [streamEncoder.flush];
// anything else first flushes them so ordering is preserved.
func (s *streamEncoder) add(ev any) []*pubsub.Payload {
	if s.opts.wire < 2 {
		if p := wrapEvent(ev); p != nil {
			return []*pubsub.Payload{p}
		}
		return nil
	}
	if m, ok := isStreamingUpdate(ev); ok {
		pm := messageToProto(m)
		if s.opts.frame > 0 {
			if _, seen := s.pending[pm.ID]; !seen {
				s.pendingOrder = append(s.pendingOrder, pm.ID)
			}
			s.pending[pm.ID] = pm
			return nil
		}
		return s.encodeMessage(pm)
	}
	out := s.flush()
	if e, ok := ev.(pubsub.Event[message.Message]); ok {
		switch e.Type {
		case pubsub.DeletedEvent:
			delete(s.last, e.Payload.ID)
			delete(s.deltas, e.Payload.ID)
		case pubsub.CreatedEvent:
			s.last[e.Payload.ID] = messageToProto(e.Payload)
			s.deltas[e.Payload.ID] = 0
		}
	}
	if p := wrapEvent(ev); p != nil {
		out = append(out, p)
	}
	return out
}

// flush emits the coalesced message updates.
func (s *streamEncoder) flush() []*pubsub.Payload {
	if len(s.pendingOrder) == 0 {
		return nil
	}
	var out []*pubsub.Payload
	for _, id := range s.pendingOrder {
		out = append(out, s.encodeMessage(s.pending[id])...)
		delete(s.pending, id)
	}
	s.pendingOrder = s.pendingOrder[:0]
	return out
}

// encodeMessage sends m as a delta against the client's copy when possible
// and as a full message otherwise.
func (s *streamEncoder) encodeMessage(m proto.Message) []*pubsub.Payload {
	prev, known := s.last[m.ID]
	s.last[m.ID] = m
	if known && s.deltas[m.ID] < keyframeEvery {
		if d, ok := proto.DiffMessage(prev, m); ok {
			if len(d.Appends) == 0 && d.UpdatedAt == prev.UpdatedAt {
				return nil
			}
			s.deltas[m.ID]++
			return []*pubsub.Payload{deltaEnvelope(d)}
		}
	}
	s.deltas[m.ID] = 0
	if m.IsFinished() {
		// Finished messages stop changing; drop them from the cache.
		defer delete(s.last, m.ID)
		defer delete(s.deltas, m.ID)
	}
	return []*pubsub.Payload{envelope(pubsub.PayloadTypeMessage, pubsub.Event[proto.Message]{Type: pubsub.UpdatedEvent, Payload: m})}
}

func deltaEnvelope(d proto.MessageDelta) *pubsub.Payload {
	raw, err := json.Marshal(d)
	if err != nil {
		slog.Error("Failed to marshal message delta", "error", err)
		return nil
	}
	return &pubsub.Payload{Type: pubsub.PayloadTypeMessageDelta, Payload: raw}
}
