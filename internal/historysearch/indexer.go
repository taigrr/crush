package historysearch

import (
	"context"
	"log/slog"

	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/message"
)

// RunIndexer subscribes to message events and embeds finished messages
// in the background under the active signature. It is a no-op when the
// embedder is disabled. It blocks until ctx is cancelled, so callers
// should run it in a goroutine.
//
// Embedding happens off the write path: failures are logged and never
// block message persistence. Already-embedded messages are skipped via
// HasVector, so the streaming UpdatedEvent storm collapses to a single
// embed once a message is finished.
func RunIndexer(ctx context.Context, messages message.Service, emb embedding.Service) {
	if emb == nil || !emb.Enabled() {
		return
	}
	// Reconcile drops vectors left over from a previous embedder
	// (signature mismatch) so stale rows never pollute search.
	if err := emb.Reconcile(ctx); err != nil {
		slog.Warn("Embedding reconcile failed", "error", err)
	}

	events := messages.Subscribe(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			indexMessage(ctx, emb, event.Payload)
		}
	}
}

// indexMessage embeds one message if it is a finished, embeddable role
// and not already embedded under the active signature.
func indexMessage(ctx context.Context, emb embedding.Service, m message.Message) {
	if !embeddableMessage(m) {
		return
	}
	body := MessageBody(m)
	if body == "" {
		return
	}
	has, err := emb.HasVector(ctx, embedding.SourceMessage, m.ID)
	if err != nil {
		slog.Debug("Embedding HasVector check failed", "message_id", m.ID, "error", err)
		return
	}
	if has {
		return
	}
	if err := emb.Embed(ctx, embedding.SourceMessage, m.ID, m.SessionID, body); err != nil {
		slog.Debug("Embedding message failed", "message_id", m.ID, "error", err)
	}
}
