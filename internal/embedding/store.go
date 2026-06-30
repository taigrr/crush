package embedding

import (
	"context"
	"database/sql"
	"time"

	"github.com/taigrr/crush/internal/db"
)

// SourceType identifies what a vector was computed from.
type SourceType string

const (
	// SourceMessage is a message body embedding (source_id = messages.id).
	SourceMessage SourceType = "message"
)

// storedVector is a decoded embedding row.
type storedVector struct {
	SourceType string
	SourceID   string
	ChunkIdx   int64
	SessionID  string
	Vec        []float32
}

// store wraps the generated queries with float32 (de)serialization.
type store struct {
	q db.Querier
}

func newStore(q db.Querier) *store { return &store{q: q} }

// upsert writes (or replaces) a single vector for the given source under
// the active signature.
func (s *store) upsert(ctx context.Context, signature string, src SourceType, sourceID, sessionID string, chunkIdx int64, vec []float32) error {
	return s.q.UpsertEmbedding(ctx, db.UpsertEmbeddingParams{
		SourceType: string(src),
		SourceID:   sourceID,
		ChunkIdx:   chunkIdx,
		Signature:  signature,
		Dim:        int64(len(vec)),
		Vec:        encodeVector(vec),
		SessionID:  nullString(sessionID),
		CreatedAt:  time.Now().UnixMilli(),
	})
}

// has reports whether a vector already exists for the source under the
// signature (used to skip re-embedding).
func (s *store) has(ctx context.Context, signature string, src SourceType, sourceID string, chunkIdx int64) (bool, error) {
	n, err := s.q.HasEmbedding(ctx, db.HasEmbeddingParams{
		SourceType: string(src),
		SourceID:   sourceID,
		ChunkIdx:   chunkIdx,
		Signature:  signature,
	})
	return n > 0, err
}

// listBySignature returns all decoded vectors for the active signature,
// optionally scoped to a session.
func (s *store) listBySignature(ctx context.Context, signature, sessionID string) ([]storedVector, error) {
	var rows []db.Embedding
	var err error
	if sessionID != "" {
		rows, err = s.q.ListEmbeddingsBySignatureAndSession(ctx, db.ListEmbeddingsBySignatureAndSessionParams{
			Signature: signature,
			SessionID: nullString(sessionID),
		})
	} else {
		rows, err = s.q.ListEmbeddingsBySignature(ctx, signature)
	}
	if err != nil {
		return nil, err
	}
	out := make([]storedVector, 0, len(rows))
	for _, r := range rows {
		out = append(out, storedVector{
			SourceType: r.SourceType,
			SourceID:   r.SourceID,
			ChunkIdx:   r.ChunkIdx,
			SessionID:  r.SessionID.String,
			Vec:        decodeVector(r.Vec),
		})
	}
	return out, nil
}

func (s *store) countBySignature(ctx context.Context, signature string) (int64, error) {
	return s.q.CountEmbeddingsBySignature(ctx, signature)
}

func (s *store) countTotal(ctx context.Context) (int64, error) {
	return s.q.CountEmbeddingsTotal(ctx)
}

// sourceIDSet returns the set of message source ids already embedded
// under the signature, for cheap membership checks during backfill.
func (s *store) sourceIDSet(ctx context.Context, signature string) (map[string]struct{}, error) {
	ids, err := s.q.ListSourceIDsForSignature(ctx, db.ListSourceIDsForSignatureParams{
		SourceType: string(SourceMessage),
		Signature:  signature,
	})
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set, nil
}

// dropStale removes every vector not matching the active signature.
func (s *store) dropStale(ctx context.Context, activeSignature string) error {
	return s.q.DeleteEmbeddingsExceptSignature(ctx, activeSignature)
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
