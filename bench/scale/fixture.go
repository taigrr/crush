// Package scale generates deterministic many-workspace / many-session
// fixtures for scale benchmarks.
package scale

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	"github.com/taigrr/crush/internal/message"
)

// Fill inserts nSess sessions (longN of them with longMsgs messages, the
// rest with msgs) into the migrated database at path. Deterministic.
func Fill(path string, ws, nSess, msgs, longN, longMsgs int, base int64) error {
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(OFF)")
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx := context.Background()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	sIns, err := tx.PrepareContext(ctx, `INSERT INTO sessions (id, title, working_dir, created_at, updated_at, last_finished_at, last_seen_at) VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer sIns.Close()
	mIns, err := tx.PrepareContext(ctx, `INSERT INTO messages (id, session_id, role, parts, model, provider, created_at, updated_at, finished_at) VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer mIns.Close()
	for s := range nSess {
		id := fmt.Sprintf("w%04d-s%05d", ws, s)
		t := base + int64(s)*60
		seen := t
		if s%10 == 0 {
			seen = 0
		}
		if _, err := sIns.ExecContext(ctx, id, fmt.Sprintf("Session %d of workspace %d: refactor something", s, ws), "", t, t, t, seen); err != nil {
			return err
		}
		n := msgs
		if s < longN {
			n = longMsgs
		}
		for m := range n {
			role := "user"
			if m%2 == 1 {
				role = "assistant"
			}
			parts, err := message.MarshalParts([]message.ContentPart{message.TextContent{
				Text: fmt.Sprintf("Message %d in %s. Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.", m, id),
			}})
			if err != nil {
				return err
			}
			mt := t + int64(m)
			if _, err := mIns.ExecContext(ctx, fmt.Sprintf("%s-m%04d", id, m), id, role, string(parts), "m", "fake", mt, mt, mt); err != nil {
				return err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}

// CopyFile copies src to dst.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
