package historysearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

// TestSearch_IncludesArchivedSessions verifies that a query matching a
// message in an archived session still surfaces the hit AND labels it with
// the archived session's title (rather than "(untitled)").
func TestSearch_IncludesArchivedSessions(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()

	dataDir := t.TempDir()
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)

	q := db.New(conn)
	sessions := session.NewService(q, conn)
	messages := message.NewService(q)

	sess, err := sessions.Create(ctx, "Archived Project Notes")
	require.NoError(t, err)
	_, err = messages.Create(ctx, sess.ID, message.CreateMessageParams{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "notes about the monorepo layout"}},
	})
	require.NoError(t, err)
	require.NoError(t, sessions.Archive(ctx, sess.ID))

	emb := embedding.Build(q, embedding.Params{})
	res, err := Search(ctx, messages, sessions, emb, "monorepo", Options{Scope: ScopeAll, Limit: 10})
	require.NoError(t, err)

	require.Equal(t, 1, res.Total, "archived session message must be searchable")
	require.Equal(t, "Archived Project Notes", res.Hits[0].SessionTitle,
		"archived session hit must carry its title")
}
