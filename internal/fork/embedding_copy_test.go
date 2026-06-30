package fork

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

// TestCopyMessagesUpToReplaysEmbeddings verifies that forking copies the
// stored embedding vectors onto the new message ids (no re-embedding),
// up to but not including the fork-point message.
func TestCopyMessagesUpToReplaysEmbeddings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	conn, err := db.Connect(ctx, t.TempDir())
	require.NoError(t, err)
	q := db.New(conn)

	sessions := session.NewService(q, conn)
	messages := message.NewService(q)
	svc := &service{queries: q, conn: conn, sessions: sessions, messages: messages}

	src, err := sessions.Create(ctx, "source")
	require.NoError(t, err)

	m1, err := messages.Create(ctx, src.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
	})
	require.NoError(t, err)
	forkPoint, err := messages.Create(ctx, src.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "fork here"}},
	})
	require.NoError(t, err)

	// Embed m1 (the message that will be copied).
	vec := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	require.NoError(t, q.UpsertEmbedding(ctx, db.UpsertEmbeddingParams{
		SourceType: "message",
		SourceID:   m1.ID,
		ChunkIdx:   0,
		Signature:  "sig-1",
		Dim:        2,
		Vec:        vec,
		SessionID:  sql.NullString{String: src.ID, Valid: true},
		CreatedAt:  123,
	}))

	dst, err := sessions.Create(ctx, "fork")
	require.NoError(t, err)

	idMapping, prefill, err := svc.copyMessagesUpTo(ctx, src.ID, dst.ID, forkPoint.ID)
	require.NoError(t, err)
	require.Equal(t, "fork here", prefill)
	newID, ok := idMapping[m1.ID]
	require.True(t, ok, "m1 should have been copied")

	// The new message must carry a copied embedding with the same vector,
	// signature, and dim, but the new session id — and no API call.
	rows, err := q.ListEmbeddingsBySignature(ctx, "sig-1")
	require.NoError(t, err)

	var copied *db.Embedding
	for i := range rows {
		if rows[i].SourceID == newID {
			copied = &rows[i]
		}
	}
	require.NotNil(t, copied, "embedding should have been replayed onto the new message id")
	require.Equal(t, vec, copied.Vec)
	require.Equal(t, int64(2), copied.Dim)
	require.Equal(t, dst.ID, copied.SessionID.String)

	// Two embeddings total now: original + the fork copy.
	total, err := q.CountEmbeddingsBySignature(ctx, "sig-1")
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
}
