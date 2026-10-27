package backend

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/registry"
	"github.com/taigrr/crush/internal/transfer"
)

type fakeBridge struct {
	published []proto.AttentionEvent
	lookup    []SwarmLookupResult
	sent      []SwarmLookupResult
	sentPart  proto.SwarmMessage
}

func (f *fakeBridge) NodeID() string   { return "self" }
func (f *fakeBridge) NodeName() string { return "laptop" }
func (f *fakeBridge) PublishAttention(ev proto.AttentionEvent) {
	f.published = append(f.published, ev)
}

func (f *fakeBridge) SwarmLookup(context.Context, string) ([]SwarmLookupResult, error) {
	return f.lookup, nil
}

func (f *fakeBridge) SwarmSend(_ context.Context, t SwarmLookupResult, p proto.SwarmMessage, _ proto.PermissionPolicy) (SwarmSendResult, error) {
	f.sent = append(f.sent, t)
	f.sentPart = p
	return SwarmSendResult{SessionID: t.SessionID, Delivery: "sent"}, nil
}

func (f *fakeBridge) RemoteOverviews(context.Context) []proto.WorkspaceOverview {
	return []proto.WorkspaceOverview{{Root: "/remote", NodeID: "mini", NodeName: "mini"}}
}

func TestClusterBridgeRoutesSwarmAndAttention(t *testing.T) {
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))
	fb := &fakeBridge{}
	b.SetClusterBridge(fb)
	ctx := t.Context()

	// No local match: the lookup falls through to other nodes.
	const sid = "0f8b1c4e-1111-4222-8333-944455556666"
	fb.lookup = []SwarmLookupResult{{NodeID: "mini", WorkspaceID: "w", SessionID: sid}}
	res, err := b.LookupSwarmAddress(ctx, sid)
	require.NoError(t, err)
	require.Equal(t, "mini", res.NodeID)

	// Two remote matches are ambiguous.
	fb.lookup = append(fb.lookup, SwarmLookupResult{NodeID: "mini2", SessionID: sid})
	_, err = b.LookupSwarmAddress(ctx, sid)
	require.ErrorIs(t, err, ErrSwarmAddressAmbiguous)

	// Local-only lookups never consult the cluster.
	_, err = b.LookupLocalSwarmAddress(ctx, sid)
	require.ErrorIs(t, err, ErrSwarmAddressNotFound)

	// Sends to a remote target go through the bridge with a re-stamped
	// (here: unknown, so blank) sender, never the caller-supplied one.
	out, err := b.SwarmSend(ctx, "", res, proto.SwarmMessage{Text: "hi", SenderColor: "spoofed"})
	require.NoError(t, err)
	require.Equal(t, "sent", out.Delivery)
	require.Len(t, fb.sent, 1)
	require.Empty(t, fb.sentPart.SenderColor)

	// Locally originated attention is forwarded once, tagged; remote
	// events are not re-forwarded.
	b.PublishClusterJoin("r1", "mini3")
	require.Len(t, fb.published, 1)
	require.Equal(t, "self", fb.published[0].NodeID)
	b.IngestRemoteAttention(proto.AttentionEvent{NodeID: "mini", Kind: proto.AttentionBusy})
	require.Len(t, fb.published, 1)

	ovs, err := b.ListClusterOverviews(ctx)
	require.NoError(t, err)
	require.Equal(t, "mini", ovs[len(ovs)-1].NodeID)
}

func TestImportTransferDBOnlyRegistersWorkspace(t *testing.T) {
	t.Cleanup(db.ResetPool)
	ctx := t.Context()
	b, _ := newTestBackend(t)
	b.registry = registry.NewWithPath(filepath.Join(t.TempDir(), "workspaces.jsonl"))

	srcData := t.TempDir()
	conn, err := db.Connect(ctx, srcData)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(srcData) })
	now := time.Now().UnixMilli()
	_, err = conn.Exec(`INSERT INTO sessions (id, title, message_count, prompt_tokens, completion_tokens, cost, updated_at, created_at, owner_node, moved_to_node) VALUES ('s1','Moved',0,0,0,0,?,?,'other-node', 'laptop')`, now, now)
	require.NoError(t, err)

	dest := filepath.Join(t.TempDir(), "proj")
	var buf bytes.Buffer
	require.NoError(t, transfer.Export(ctx, &buf, transfer.ExportParams{
		DB:       conn,
		Plan:     transfer.PlanDBOnly,
		Manifest: transfer.Manifest{SessionID: "s1", Root: dest, DataDirRel: ".crush", SourceNodeName: "laptop"},
	}))

	pre, err := b.TransferPreflight(ctx, proto.TransferPreflightRequest{Root: dest, SessionID: "s1", SchemaVersion: db.LatestSchemaVersion()})
	require.NoError(t, err)
	require.Equal(t, transfer.PlanFresh, pre.Plan)

	res, err := b.ImportTransfer(ctx, &buf, transfer.PlanDBOnly, false)
	require.NoError(t, err)
	require.Equal(t, dest, res.Root)

	ro, err := db.OpenReadOnly(filepath.Join(dest, ".crush"))
	require.NoError(t, err)
	defer ro.Close()
	var owner, moved sql.NullString
	require.NoError(t, ro.QueryRow(`SELECT owner_node, moved_to_node FROM sessions WHERE id='s1'`).Scan(&owner, &moved))
	require.False(t, owner.Valid, "the receiving node owns the session")
	require.False(t, moved.Valid)

	entries, err := b.registry.List()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, dest, entries[0].Root)

	// A second move onto this node while it owns the session is refused.
	_, err = b.TransferPreflight(ctx, proto.TransferPreflightRequest{Root: dest, SessionID: "s1", SchemaVersion: db.LatestSchemaVersion()})
	require.ErrorIs(t, err, transfer.ErrSessionOwned)

	_, err = b.TransferPreflight(ctx, proto.TransferPreflightRequest{Root: dest, SessionID: "s1", SchemaVersion: 1})
	require.ErrorIs(t, err, transfer.ErrSchemaMismatch)
}
