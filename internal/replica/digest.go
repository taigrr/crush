package replica

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"maps"
	"slices"

	"github.com/taigrr/crush/internal/transfer"
)

// digestIgnored lists columns left out of a digest because they
// legitimately differ between an owner and its replicas: ownership is
// stamped on serve, and updated_at is rewritten by local triggers.
var digestIgnored = []string{"owner_node", "moved_to_node", "updated_at"}

// OwnedDigest hashes every session self owns. Peers compare it with
// [PeerDigest] to find drifted sessions without shipping their rows.
func OwnedDigest(ctx context.Context, db *sql.DB, self string) (map[string]string, error) {
	owned, err := ownedIDs(ctx, db, self)
	if err != nil {
		return nil, err
	}
	return digest(ctx, db, slices.Sorted(maps.Keys(owned)))
}

// PeerDigest hashes every replicated session owned by peer.
func PeerDigest(ctx context.Context, db *sql.DB, peer string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT id FROM sessions WHERE owner_node = ?`, peer)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return digest(ctx, db, ids)
}

// Drifted returns the owner's sessions whose local digest differs or is
// missing, sorted.
func Drifted(owner, local map[string]string) []string {
	var out []string
	for id, h := range owner {
		if local[id] != h {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

func digest(ctx context.Context, db *sql.DB, ids []string) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	lines := map[string][]string{}
	err := transfer.ExportRows(ctx, db, ids, func(r transfer.Row) error {
		if r.Table == "embeddings" {
			return nil
		}
		key := "session_id"
		if r.Table == "sessions" {
			key = "id"
		}
		sid := valueString(r.Cols[key])
		cols := maps.Clone(r.Cols)
		for _, c := range digestIgnored {
			delete(cols, c)
		}
		data, err := json.Marshal(cols)
		if err != nil {
			return err
		}
		lines[sid] = append(lines[sid], r.Table+"\x00"+string(data))
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		l := lines[id]
		slices.Sort(l)
		h := sha256.New()
		for _, s := range l {
			h.Write([]byte(s))
			h.Write([]byte{'\n'})
		}
		out[id] = hex.EncodeToString(h.Sum(nil)[:16])
	}
	return out, nil
}

// ServeSessions returns a repair batch with the current rows of the
// given sessions that self owns. Embeddings are left to the rowid
// cursor. A repair batch never moves the peer's cursors.
func ServeSessions(ctx context.Context, db *sql.DB, self string, ids []string) (Batch, error) {
	owned, err := ownedIDs(ctx, db, self)
	if err != nil {
		return Batch{}, err
	}
	var keep []string
	for _, id := range ids {
		if owned[id] {
			keep = append(keep, id)
		}
	}
	b := Batch{Repair: true}
	if len(keep) == 0 {
		return b, nil
	}
	err = transfer.ExportRows(ctx, db, keep, func(r transfer.Row) error {
		if r.Table == "embeddings" {
			return nil
		}
		if r.Table == "sessions" {
			stampOwner(r.Cols, self)
		}
		b.Changes = append(b.Changes, Change{Op: "U", Table: r.Table, Row: r.Cols})
		return nil
	})
	return b, err
}
