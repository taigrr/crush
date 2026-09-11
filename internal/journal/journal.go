// Package journal persists the agent's transient per-session state —
// the prompt queue and outstanding swarm reply obligations — so a
// server that drains and exits for a binary update leaves that state on
// disk for the next server to rehydrate.
//
// The in-memory structures (the sessionAgent queue, swarm.ReplyTracker)
// remain the source of truth at runtime; the journal is written through
// on every mutation with the full current snapshot for the affected
// session and read back once when a workspace comes up.
//
// Callback-only state cannot be persisted. A queued call carries an
// OnComplete hook and an accept reservation that belong to the process
// that accepted it; a rehydrated prompt therefore runs as an ordinary
// turn with a fresh run id and no waiter.
package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/swarm"
)

// QueuedPrompt is the persistable projection of a queued agent call.
type QueuedPrompt struct {
	SessionID   string
	RunID       string
	Prompt      string
	Attachments []message.Attachment
	SwarmParts  []message.SwarmMessage
	// CreatedAt is when the row was journaled. Zero on entries that
	// have not been persisted yet.
	CreatedAt time.Time
}

// Store journals queue and reply-obligation state into the workspace
// database.
type Store struct {
	conn    *sql.DB
	q       *db.Queries
	dataDir string
}

// New returns a Store backed by the given connection. dataDir is the
// workspace data directory the hand-off marker is written into; empty
// disables the marker.
func New(conn *sql.DB, q *db.Queries, dataDir string) *Store {
	return &Store{conn: conn, q: q, dataDir: dataDir}
}

// handoffMarker is the file a draining server writes next to the
// database right before it exits, so the next server can tell a clean
// hand-off (replay everything) from rows left behind by a crash or a
// forced kill (replay only what is still fresh).
const handoffMarker = "queue-handoff"

// ReplayTTL bounds how old a journaled prompt may be and still be
// replayed without a hand-off marker. Exposed so tests can shorten it.
var ReplayTTL = 24 * time.Hour

// MarkHandoff records that the journal was left intact deliberately by
// a draining server. Best-effort: a failure only means the next server
// applies the freshness rule instead.
func (s *Store) MarkHandoff() error {
	if s == nil || s.dataDir == "" {
		return nil
	}
	path := filepath.Join(s.dataDir, handoffMarker)
	return os.WriteFile(path, []byte(strconv.FormatInt(time.Now().UnixMilli(), 10)), 0o600)
}

// HandoffPending reports whether a hand-off marker is present without
// consuming it. Reply-obligation hydration runs before queue replay and
// must observe the same marker the replay later consumes.
func (s *Store) HandoffPending() bool {
	if s == nil || s.dataDir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(s.dataDir, handoffMarker))
	return err == nil
}

// ConsumeHandoff reports whether a hand-off marker was present and
// removes it, so it applies to exactly one replay.
func (s *Store) ConsumeHandoff() bool {
	if s == nil || s.dataDir == "" {
		return false
	}
	path := filepath.Join(s.dataDir, handoffMarker)
	if _, err := os.Stat(path); err != nil {
		return false
	}
	err := os.Remove(path)
	return err == nil || errors.Is(err, fs.ErrNotExist)
}

// Fresh reports whether a journaled prompt is recent enough to replay
// without a hand-off marker.
func (e QueuedPrompt) Fresh(now time.Time) bool {
	return !e.CreatedAt.IsZero() && now.Sub(e.CreatedAt) <= ReplayTTL
}

// writeTimeout bounds every journal write. Writes run on a detached
// context because the run context that triggered them may already be
// canceled (e.g. a queue drop during workspace teardown).
const writeTimeout = 5 * time.Second

// SaveQueue replaces the persisted queue for sessionID with entries. An
// empty slice deletes the session's rows.
func (s *Store) SaveQueue(ctx context.Context, sessionID string, entries []QueuedPrompt) error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin queue journal tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	q := s.q.WithTx(tx)
	if err := q.DeleteSessionQueue(ctx, sessionID); err != nil {
		return fmt.Errorf("clear queue journal: %w", err)
	}
	now := time.Now().UnixMilli()
	for i, e := range entries {
		attachments, err := json.Marshal(e.Attachments)
		if err != nil {
			return fmt.Errorf("encode attachments: %w", err)
		}
		var parts sql.NullString
		if len(e.SwarmParts) > 0 {
			raw, err := json.Marshal(e.SwarmParts)
			if err != nil {
				return fmt.Errorf("encode swarm parts: %w", err)
			}
			parts = sql.NullString{String: string(raw), Valid: true}
		}
		if err := q.InsertSessionQueueEntry(ctx, db.InsertSessionQueueEntryParams{
			SessionID:   sessionID,
			Seq:         int64(i),
			RunID:       sql.NullString{String: e.RunID, Valid: e.RunID != ""},
			Prompt:      e.Prompt,
			Attachments: string(attachments),
			SwarmParts:  parts,
			CreatedAt:   now,
		}); err != nil {
			return fmt.Errorf("write queue journal: %w", err)
		}
	}
	return tx.Commit()
}

// LoadQueue returns every persisted queued prompt, grouped by session in
// queue order.
func (s *Store) LoadQueue(ctx context.Context) (map[string][]QueuedPrompt, error) {
	if s == nil {
		return nil, nil
	}
	rows, err := s.q.ListSessionQueue(ctx)
	if err != nil {
		return nil, fmt.Errorf("read queue journal: %w", err)
	}
	out := make(map[string][]QueuedPrompt)
	for _, r := range rows {
		e := QueuedPrompt{
			SessionID: r.SessionID,
			RunID:     r.RunID.String,
			Prompt:    r.Prompt,
			CreatedAt: time.UnixMilli(r.CreatedAt),
		}
		if r.Attachments != "" {
			if err := json.Unmarshal([]byte(r.Attachments), &e.Attachments); err != nil {
				return nil, fmt.Errorf("decode attachments for session %s: %w", r.SessionID, err)
			}
		}
		if r.SwarmParts.Valid && r.SwarmParts.String != "" {
			if err := json.Unmarshal([]byte(r.SwarmParts.String), &e.SwarmParts); err != nil {
				return nil, fmt.Errorf("decode swarm parts for session %s: %w", r.SessionID, err)
			}
		}
		out[r.SessionID] = append(out[r.SessionID], e)
	}
	return out, nil
}

// ClearQueues deletes every persisted queued prompt. Called once the
// rows have been handed to the live agent on rehydration; the agent
// re-journals whatever it actually enqueues.
func (s *Store) ClearQueues(ctx context.Context) error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	return s.q.DeleteAllSessionQueues(ctx)
}

// SaveReplies implements [swarm.ReplyJournal].
func (s *Store) SaveReplies(sessionID string, obs []swarm.ReplyObligation) error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reply journal tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck
	q := s.q.WithTx(tx)
	if err := q.DeleteSwarmReplyObligations(ctx, sessionID); err != nil {
		return fmt.Errorf("clear reply journal: %w", err)
	}
	now := time.Now().UnixMilli()
	for i, ob := range obs {
		if err := q.InsertSwarmReplyObligation(ctx, db.InsertSwarmReplyObligationParams{
			ObligatedSessionID: sessionID,
			OwedToSessionID:    ob.SenderSessionID,
			OwedToWorkspaceID:  ob.SenderWorkspaceID,
			OwedToAddress:      ob.SenderAddress,
			Body:               ob.Body,
			Nudges:             int64(ob.Nudges),
			Undelivered:        boolInt(ob.Undelivered),
			// Offset so insertion order survives the ORDER BY on load
			// even when several rows share a millisecond.
			CreatedAt: now + int64(i),
		}); err != nil {
			return fmt.Errorf("write reply journal: %w", err)
		}
	}
	return tx.Commit()
}

// LoadReplies implements [swarm.ReplyJournal]. Without a pending
// hand-off marker the rows were left by a crash or forced stop, so those
// older than ReplayTTL are dropped (and deleted) rather than hydrated —
// nudging a sender about a days-old message would be a surprise.
func (s *Store) LoadReplies() (map[string][]swarm.ReplyObligation, error) {
	if s == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	rows, err := s.q.ListSwarmReplyObligations(ctx)
	if err != nil {
		return nil, fmt.Errorf("read reply journal: %w", err)
	}
	handedOff := s.HandoffPending()
	now := time.Now()
	stale := make(map[string]struct{})
	out := make(map[string][]swarm.ReplyObligation)
	for _, r := range rows {
		if !handedOff && now.Sub(time.UnixMilli(r.CreatedAt)) > ReplayTTL {
			stale[r.ObligatedSessionID] = struct{}{}
			continue
		}
		out[r.ObligatedSessionID] = append(out[r.ObligatedSessionID], swarm.ReplyObligation{
			SenderSessionID:   r.OwedToSessionID,
			SenderWorkspaceID: r.OwedToWorkspaceID,
			SenderAddress:     r.OwedToAddress,
			Body:              r.Body,
			Nudges:            int(r.Nudges),
			Undelivered:       r.Undelivered != 0,
		})
	}
	for sessionID := range stale {
		// Rewrite the session with only its fresh rows (possibly none).
		if err := s.SaveReplies(sessionID, out[sessionID]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ClearReplies deletes every persisted reply obligation. Used by a
// forced shutdown, whose in-flight turns have already told their senders
// the work was cancelled.
func (s *Store) ClearReplies(ctx context.Context) error {
	if s == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), writeTimeout)
	defer cancel()
	return s.q.DeleteAllSwarmReplyObligations(ctx)
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
