package pubsub

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// BenchmarkBrokerPublish measures fan-out of one event to several draining
// subscribers, the path every message/session update takes to the UI and
// server streams. Subscribers can fall behind, so drop warnings are
// discarded to keep benchmark output parseable.
func BenchmarkBrokerPublish(b *testing.B) {
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(prev)

	broker := NewBroker[string]()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for range 4 {
		ch := broker.Subscribe(ctx)
		go func() {
			for range ch {
			}
		}()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		broker.Publish(UpdatedEvent, "payload")
	}
	b.StopTimer()
	broker.Shutdown()
}
