package message_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/benchdata"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

// BenchmarkMessageList measures loading a full session history from SQLite
// and decoding every row into message.Message, which is what the TUI and
// server do when a session is opened.
func BenchmarkMessageList(b *testing.B) {
	for _, size := range []int{500, 2000} {
		b.Run(fmt.Sprintf("msgs=%d", size), func(b *testing.B) {
			ctx := b.Context()
			conn, err := db.Connect(ctx, db.TempDataDir(b))
			require.NoError(b, err)
			queries := db.New(conn)
			sess, err := session.NewService(queries, conn).Create(ctx, "bench")
			require.NoError(b, err)
			svc := message.NewService(queries)
			for _, msg := range benchdata.Messages(sess.ID, size) {
				_, err := svc.Create(ctx, sess.ID, message.CreateMessageParams{
					Role:     msg.Role,
					Parts:    msg.Parts,
					Model:    msg.Model,
					Provider: msg.Provider,
				})
				require.NoError(b, err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				msgs, err := svc.List(ctx, sess.ID)
				if err != nil {
					b.Fatal(err)
				}
				if len(msgs) != size {
					b.Fatalf("got %d messages, want %d", len(msgs), size)
				}
			}
		})
	}
}
