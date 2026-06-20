package sync

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Metadata keys stored in the sync_metadata table. Mirrored here so
// callers don't pass raw strings.
const (
	keyDBID         = "db_id"
	keyFingerprint  = "project_fingerprint"
	keyChangeSeq    = "change_seq"
	keyPushCursor   = "push_cursor"
	keyPullCursor   = "pull_cursor"
	keyLastSyncAt   = "last_sync_at"
	keyPortableHint = "fingerprint_portable"
)

// Identity is the per-database sync identity persisted in
// sync_metadata. Loaded once at Connect time.
type Identity struct {
	DBID        string
	Fingerprint string
	Portable    bool // false when fingerprint is path-derived (no git remote)
}

// FingerprintInputs are the values fed into the project fingerprint
// hash. Both fields may be empty; if Remote is empty we fall back to
// hashing AbsPath and mark the identity non-portable.
type FingerprintInputs struct {
	Remote          string // normalized git remote, e.g. "github.com/charm/crush"
	RepoRelCrushDir string // path of the .crush dir relative to repo root
	AbsPath         string // absolute path of the .crush dir, used as fallback
}

// Fingerprint returns the SHA256-based project fingerprint per
// docs/sync-spec.md §4. The portable bool is true iff a remote was
// supplied (i.e. the fingerprint is stable across machines).
func Fingerprint(in FingerprintInputs) (fp string, portable bool) {
	var h [32]byte
	if in.Remote != "" {
		h = sha256.Sum256([]byte(strings.ToLower(in.Remote) + ":" + filepath.ToSlash(in.RepoRelCrushDir)))
		return hex.EncodeToString(h[:]), true
	}
	h = sha256.Sum256([]byte(filepath.ToSlash(in.AbsPath)))
	return hex.EncodeToString(h[:]), false
}

// LoadOrInitIdentity returns the persisted identity, creating db_id
// and fingerprint on first call. The fingerprint inputs are only
// consulted when no fingerprint is yet stored; subsequent calls return
// whatever was persisted (the fingerprint is the DO routing key and
// must not change for the lifetime of a database).
func LoadOrInitIdentity(ctx context.Context, db *sql.DB, in FingerprintInputs) (Identity, error) {
	var id Identity

	if v, ok, err := getMeta(ctx, db, keyDBID); err != nil {
		return id, err
	} else if ok {
		id.DBID = v
	} else {
		id.DBID = uuid.NewString()
		if err := setMeta(ctx, db, keyDBID, id.DBID); err != nil {
			return id, err
		}
	}

	if v, ok, err := getMeta(ctx, db, keyFingerprint); err != nil {
		return id, err
	} else if ok {
		id.Fingerprint = v
		if p, _, _ := getMeta(ctx, db, keyPortableHint); p == "1" {
			id.Portable = true
		}
	} else {
		fp, portable := Fingerprint(in)
		if fp == "" {
			return id, errors.New("sync: cannot derive project fingerprint: no inputs")
		}
		id.Fingerprint = fp
		id.Portable = portable
		if err := setMeta(ctx, db, keyFingerprint, fp); err != nil {
			return id, err
		}
		flag := "0"
		if portable {
			flag = "1"
		}
		if err := setMeta(ctx, db, keyPortableHint, flag); err != nil {
			return id, err
		}
	}
	return id, nil
}

// Cursors holds the two sync cursors and the local change_seq.
type Cursors struct {
	ChangeSeq  int64
	PushCursor int64
	PullCursor int64
}

// LoadCursors reads the three counters that drive the protocol.
func LoadCursors(ctx context.Context, db *sql.DB) (Cursors, error) {
	var c Cursors
	pairs := []struct {
		k string
		p *int64
	}{
		{keyChangeSeq, &c.ChangeSeq},
		{keyPushCursor, &c.PushCursor},
		{keyPullCursor, &c.PullCursor},
	}
	for _, p := range pairs {
		v, ok, err := getMeta(ctx, db, p.k)
		if err != nil {
			return c, err
		}
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return c, fmt.Errorf("sync: bad %s in sync_metadata: %w", p.k, err)
		}
		*p.p = n
	}
	return c, nil
}

// SetPushCursor advances the push cursor after a successful push.
func SetPushCursor(ctx context.Context, db *sql.DB, seq int64) error {
	return setMeta(ctx, db, keyPushCursor, strconv.FormatInt(seq, 10))
}

// SetPullCursor advances the pull cursor after applying remote
// changes locally.
func SetPullCursor(ctx context.Context, db *sql.DB, seq int64) error {
	return setMeta(ctx, db, keyPullCursor, strconv.FormatInt(seq, 10))
}

// SetLastSyncAt records the wall-clock of the last successful sync.
func SetLastSyncAt(ctx context.Context, db *sql.DB, epochSec int64) error {
	return setMeta(ctx, db, keyLastSyncAt, strconv.FormatInt(epochSec, 10))
}

func getMeta(ctx context.Context, db *sql.DB, key string) (string, bool, error) {
	var v string
	err := db.QueryRowContext(ctx,
		`SELECT value FROM sync_metadata WHERE key = ?`, key).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, err
	}
	return v, true, nil
}

func setMeta(ctx context.Context, db *sql.DB, key, value string) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO sync_metadata(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
