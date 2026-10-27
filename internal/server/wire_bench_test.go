package server

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pubsub"
)

// Wire-format benchmark for streamed assistant messages.
//
// Today every streamed delta republishes the whole message, and the server
// sends it to each client as one SSE JSON event. This compares that with
// alternatives: append-only deltas, a capped frame rate (coalescing), and
// gzip on the stream. Run the report with:
//
//	CRUSH_WIRE_REPORT=1 go test ./internal/server -run TestWireFormatReport -v

// wireScenario describes one streamed assistant turn.
type wireScenario struct {
	name      string
	reasoning int // characters
	text      int
	toolInput int
	chunk     int // characters per provider delta (~1 token ≈ 4 chars)
}

var wireScenarios = []wireScenario{
	{name: "short answer", reasoning: 400, text: 1200, chunk: 4},
	{name: "typical turn", reasoning: 1500, text: 6000, toolInput: 800, chunk: 4},
	{name: "long answer", reasoning: 4000, text: 24000, toolInput: 3000, chunk: 4},
}

// prose is deterministic, mildly repetitive filler resembling model
// output (compresses like real text, not like random bytes).
func prose(n int) string {
	words := strings.Fields(`the server streams each update to the client so the user sees
		the answer appear token by token while tools run and files change in the
		project we keep state consistent across machines and reconnect cleanly when
		the network drops func main returns error nil if err != nil { return err }`)
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		b.WriteString(words[(i*7+i/3)%len(words)])
		b.WriteByte(' ')
	}
	return b.String()[:n]
}

// streamUpdates replays a turn as the sequence of message states the
// message service publishes (one per provider delta).
func streamUpdates(s wireScenario) []message.Message {
	reasoning, text, input := prose(s.reasoning), prose(s.text), prose(s.toolInput)
	msg := message.Message{
		ID:        "6f3c1f0a-6c64-4f54-9f6b-8a1d2c3e4f5a",
		SessionID: "0d9e8f7a-1b2c-4d3e-8f90-a1b2c3d4e5f6",
		Role:      message.Assistant,
		Model:     "claude-opus",
		Provider:  "bedrock",
		CreatedAt: time.Now().Unix(),
	}
	var out []message.Message
	emit := func() {
		cp := msg
		cp.Parts = append([]message.ContentPart(nil), msg.Parts...)
		out = append(out, cp)
	}
	msg.Parts = []message.ContentPart{message.ReasoningContent{}}
	for i := s.chunk; i <= len(reasoning); i += s.chunk {
		msg.Parts[0] = message.ReasoningContent{Thinking: reasoning[:i]}
		emit()
	}
	msg.Parts = append(msg.Parts, message.TextContent{})
	for i := s.chunk; i <= len(text); i += s.chunk {
		msg.Parts[1] = message.TextContent{Text: text[:i]}
		emit()
	}
	if s.toolInput > 0 {
		msg.Parts = append(msg.Parts, message.ToolCall{ID: "call_1", Name: "edit"})
		for i := s.chunk * 4; i <= len(input); i += s.chunk * 4 {
			msg.Parts[2] = message.ToolCall{ID: "call_1", Name: "edit", Input: input[:i]}
			emit()
		}
	}
	return out
}

// fullEvents encodes each update the way the server does today: the
// whole message in the SSE envelope.
func fullEvents(updates []message.Message) [][]byte {
	out := make([][]byte, 0, len(updates))
	for _, m := range updates {
		p := wrapEvent(pubsub.Event[message.Message]{Type: pubsub.UpdatedEvent, Payload: m})
		data, _ := json.Marshal(p)
		out = append(out, append(append([]byte("data: "), data...), '\n', '\n'))
	}
	return out
}

// wireDelta is a candidate append-only update.
type wireDelta struct {
	Seq    int    `json:"s"`
	ID     string `json:"m"`
	Part   int    `json:"p"`
	Kind   string `json:"k,omitempty"`
	Append string `json:"a"`
}

// partText extracts the growable text of a part.
func partText(p message.ContentPart) (string, string) {
	switch v := p.(type) {
	case message.ReasoningContent:
		return "r", v.Thinking
	case message.TextContent:
		return "t", v.Text
	case message.ToolCall:
		return "i", v.Input
	}
	return "", ""
}

// deltaEvents encodes only what changed since the previous update. The
// first event of a message is a full snapshot (the "keyframe").
func deltaEvents(updates []message.Message) [][]byte {
	var out [][]byte
	prev := map[int]string{}
	for i, m := range updates {
		if i == 0 {
			out = append(out, fullEvents(updates[:1])...)
		}
		for idx, part := range m.Parts {
			kind, text := partText(part)
			if text == prev[idx] {
				continue
			}
			d := wireDelta{Seq: i, ID: m.ID, Part: idx, Append: strings.TrimPrefix(text, prev[idx])}
			if _, seen := prev[idx]; !seen {
				d.Kind = kind
			}
			prev[idx] = text
			data, _ := json.Marshal(d)
			out = append(out, append(append([]byte("data: "), data...), '\n', '\n'))
		}
	}
	return out
}

// coalesce keeps every n-th update (a frame-rate cap merges the rest).
func coalesce(updates []message.Message, n int) []message.Message {
	var out []message.Message
	for i := n - 1; i < len(updates); i += n {
		out = append(out, updates[i])
	}
	if len(updates) > 0 && (len(updates)%n) != 0 {
		out = append(out, updates[len(updates)-1])
	}
	return out
}

type wireResult struct {
	events int
	raw    int
	gz     int
	encode time.Duration
}

// measure totals event bytes, and bytes after a per-connection gzip
// stream flushed after every event (SSE must deliver each promptly).
func measure(encode func() [][]byte) wireResult {
	start := time.Now()
	events := encode()
	r := wireResult{events: len(events), encode: time.Since(start)}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	for _, e := range events {
		r.raw += len(e)
		_, _ = zw.Write(e)
		_ = zw.Flush()
	}
	_ = zw.Close()
	r.gz = buf.Len()
	return r
}

func kb(n int) string { return fmt.Sprintf("%.1f KB", float64(n)/1024) }

func TestWireFormatReport(t *testing.T) {
	if os.Getenv("CRUSH_WIRE_REPORT") == "" {
		t.Skip("set CRUSH_WIRE_REPORT=1 to print the wire format comparison")
	}
	// At the observed ~260 ms RTT and an assumed ~2 MB/s effective
	// throughput over WireGuard, bytes translate into send time.
	const bytesPerSec = 2 << 20
	for _, s := range wireScenarios {
		updates := streamUpdates(s)
		final := len(fullEvents(updates[len(updates)-1:])[0])
		t.Logf("")
		t.Logf("== %s: %d updates, final message %s", s.name, len(updates), kb(final))
		t.Logf("%-34s %7s %11s %11s %9s %10s", "format", "events", "raw", "gzip", "ratio", "wire time")
		row := func(name string, r wireResult) {
			sendGz := time.Duration(float64(r.gz) / bytesPerSec * float64(time.Second))
			t.Logf("%-34s %7d %11s %11s %8.1fx %10v", name, r.events, kb(r.raw), kb(r.gz),
				float64(r.raw)/float64(max(r.gz, 1)), sendGz.Round(time.Millisecond))
		}
		row("full message per delta (today)", measure(func() [][]byte { return fullEvents(updates) }))
		row("full message, 50ms frames", measure(func() [][]byte { return fullEvents(coalesce(updates, 5)) }))
		row("append deltas", measure(func() [][]byte { return deltaEvents(updates) }))
		row("append deltas, 50ms frames", measure(func() [][]byte { return deltaEvents(coalesce(updates, 5)) }))
	}
}

func BenchmarkWireFullMessage(b *testing.B) {
	updates := streamUpdates(wireScenarios[1])
	b.ReportAllocs()
	for b.Loop() {
		_ = fullEvents(updates)
	}
}

func BenchmarkWireDeltas(b *testing.B) {
	updates := streamUpdates(wireScenarios[1])
	b.ReportAllocs()
	for b.Loop() {
		_ = deltaEvents(updates)
	}
}
