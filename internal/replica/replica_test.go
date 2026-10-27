package replica

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
)

func open(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Connect(t.Context(), dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dir) })
	return conn
}

func exec(t *testing.T, conn *sql.DB, q string, args ...any) {
	t.Helper()
	_, err := conn.Exec(q, args...)
	require.NoError(t, err)
}

func addSession(t *testing.T, conn *sql.DB, id, title string) {
	now := time.Now().UnixMilli()
	exec(t, conn, `INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at) VALUES (?,?,0,0,0,0,?,?)`, id, title, now, now)
}

func addMessage(t *testing.T, conn *sql.DB, id, sid string) {
	now := time.Now().UnixMilli()
	exec(t, conn, `INSERT INTO messages (id, session_id, role, parts, created_at, updated_at) VALUES (?,?,'user','[]',?,?)`, id, sid, now, now)
}

// roundTrip simulates the wire: JSON encode then decode.
func roundTrip(t *testing.T, b Batch) Batch {
	t.Helper()
	data, err := json.Marshal(b)
	require.NoError(t, err)
	var out Batch
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func count(t *testing.T, conn *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, conn.QueryRow(q, args...).Scan(&n))
	return n
}

func TestReplicationIsSingleOwner(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	a, b := open(t), open(t)

	addSession(t, a, "s1", "A's work")
	addMessage(t, a, "m1", "s1")
	addSession(t, b, "mine", "B's own work")

	// First pull is a full snapshot.
	batch, err := Serve(ctx, a, "nodeA", -1, 0, false)
	require.NoError(t, err)
	require.True(t, batch.Full)
	st, err := Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)
	require.Positive(t, st.Applied)
	require.NoError(t, SetCursor(ctx, b, "nodeA", batch.MaxSeq))
	var owner string
	require.NoError(t, b.QueryRow(`SELECT owner_node FROM sessions WHERE id='s1'`).Scan(&owner))
	require.Equal(t, "nodeA", owner)
	require.Equal(t, 1, count(t, b, `SELECT COUNT(*) FROM messages WHERE session_id='s1'`))

	// Incremental: a new message and a title change arrive.
	addMessage(t, a, "m2", "s1")
	exec(t, a, `UPDATE sessions SET title='renamed' WHERE id='s1'`)
	batch, err = Serve(ctx, a, "nodeA", Cursor(ctx, b, "nodeA"), 0, false)
	require.NoError(t, err)
	require.False(t, batch.Full)
	_, err = Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)
	require.Equal(t, 2, count(t, b, `SELECT COUNT(*) FROM messages WHERE session_id='s1'`))
	require.Equal(t, 1, count(t, b, `SELECT COUNT(*) FROM sessions WHERE id='s1' AND title='renamed'`))
	require.Equal(t, 2, count(t, b, `SELECT message_count FROM sessions WHERE id='s1'`))

	// B never serves A's session back (no echo loops) but serves its own.
	back, err := Serve(ctx, b, "nodeB", -1, 0, true)
	require.NoError(t, err)
	for _, c := range back.Changes {
		if c.Table == "sessions" {
			require.Equal(t, "mine", c.Row["id"].V)
		}
	}

	// A peer can never overwrite a session this node owns, even when it
	// claims ownership.
	addSession(t, a, "mine", "hijack attempt")
	batch, err = Serve(ctx, a, "nodeA", Cursor(ctx, b, "nodeA"), 0, false)
	require.NoError(t, err)
	_, err = Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)
	require.Equal(t, 1, count(t, b, `SELECT COUNT(*) FROM sessions WHERE id='mine' AND title='B''s own work'`))

	// Rows a peer does not own are not deleted by it either.
	addMessage(t, b, "bm", "mine")
	_, err = Apply(ctx, b, "nodeA", Batch{Changes: []Change{{Op: "D", Table: "messages", PK: "bm"}}})
	require.NoError(t, err)
	require.Equal(t, 1, count(t, b, `SELECT COUNT(*) FROM messages WHERE id='bm'`))

	// Owner deletes propagate.
	exec(t, a, `DELETE FROM messages WHERE id='m1'`)
	batch, err = Serve(ctx, a, "nodeA", batch.MaxSeq, 0, false)
	require.NoError(t, err)
	_, err = Apply(ctx, b, "nodeA", roundTrip(t, batch))
	require.NoError(t, err)
	require.Zero(t, count(t, b, `SELECT COUNT(*) FROM messages WHERE id='m1'`))
}

func TestEmbeddingsReplicateIncrementally(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	a, b := open(t), open(t)
	addSession(t, a, "s1", "A")
	addMessage(t, a, "m1", "s1")
	addEmbedding := func(src string) {
		exec(t, a, `INSERT INTO embeddings (source_type, source_id, chunk_idx, signature, dim, vec, session_id, created_at) VALUES ('message', ?, 0, 'sig', 2, ?, 's1', ?)`, src, []byte{1, 2, 3, 4, 5, 6, 7, 8}, time.Now().UnixMilli())
	}
	addEmbedding("m1")

	pull := func() {
		seq, emb := Cursor(ctx, b, "nodeA"), EmbedCursor(ctx, b, "nodeA")
		batch, err := Serve(ctx, a, "nodeA", seq, emb, false)
		require.NoError(t, err)
		_, err = Apply(ctx, b, "nodeA", roundTrip(t, batch))
		require.NoError(t, err)
		require.NoError(t, SetCursor(ctx, b, "nodeA", batch.MaxSeq))
		require.NoError(t, SetEmbedCursor(ctx, b, "nodeA", batch.EmbedCursor))
	}
	pull()
	require.Equal(t, 1, count(t, b, `SELECT COUNT(*) FROM embeddings`))

	// A backfill embedding arriving later, with no other change, still
	// replicates.
	addMessage(t, a, "m2", "s1")
	pull()
	addEmbedding("m2")
	pull()
	require.Equal(t, 2, count(t, b, `SELECT COUNT(*) FROM embeddings`))
	var vec []byte
	require.NoError(t, b.QueryRow(`SELECT vec FROM embeddings WHERE source_id='m2'`).Scan(&vec))
	require.Equal(t, []byte{1, 2, 3, 4, 5, 6, 7, 8}, vec)

	// Deleting a message drops its replicated embeddings.
	exec(t, a, `DELETE FROM messages WHERE id='m2'`)
	pull()
	require.Zero(t, count(t, b, `SELECT COUNT(*) FROM embeddings WHERE source_id='m2'`))
}

func TestReplicaFollowsHandoffBetweenOtherNodes(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := context.Background()
	b, c, laptop := open(t), open(t), open(t)

	// mini1 (b) owns s1; the laptop replicates it.
	addSession(t, b, "s1", "job")
	batch, err := Serve(ctx, b, "mini1", -1, 0, false)
	require.NoError(t, err)
	_, err = Apply(ctx, laptop, "mini1", roundTrip(t, batch))
	require.NoError(t, err)

	// The session moves to mini2 (c), which now serves it as owner.
	addSession(t, c, "s1", "job on mini2")
	batch, err = Serve(ctx, c, "mini2", -1, 0, false)
	require.NoError(t, err)
	_, err = Apply(ctx, laptop, "mini2", roundTrip(t, batch))
	require.NoError(t, err)

	var owner, title string
	require.NoError(t, laptop.QueryRow(`SELECT owner_node, title FROM sessions WHERE id='s1'`).Scan(&owner, &title))
	require.Equal(t, "mini2", owner)
	require.Equal(t, "job on mini2", title)

	// The old owner can no longer update the laptop's copy.
	exec(t, b, `UPDATE sessions SET title='stale' WHERE id='s1'`)
	exec(t, b, `UPDATE sessions SET owner_node='mini2' WHERE id='s1'`)
	batch, err = Serve(ctx, b, "mini1", -1, 0, true)
	require.NoError(t, err)
	_, err = Apply(ctx, laptop, "mini1", roundTrip(t, batch))
	require.NoError(t, err)
	require.NoError(t, laptop.QueryRow(`SELECT title FROM sessions WHERE id='s1'`).Scan(&title))
	require.Equal(t, "job on mini2", title)
}
