package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

func swarmSenderCtx() context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, "sender")
}

func swarmSenderSessions() stubSessionsForSwarm {
	return stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
}

func decodeSwarmMetadata(t *testing.T, raw string) SwarmResponseMetadata {
	t.Helper()
	require.NotEmpty(t, raw, "swarm result must carry metadata")
	var md SwarmResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(raw), &md))
	return md
}

// A `swarm new` call stamps the trusted sender identity (from the tool
// context, not model input) as lineage on the spawned session.
func TestSwarmNew_StampsLineageFromSender(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{}
	resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws-sender", nil, SwarmParams{
		Address: "new", Prompt: "hello",
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "sender", be.gotOpts.SpawnedBySessionID)
	require.Equal(t, "ws-sender", be.gotOpts.SpawnedByWorkspaceID)
	require.Equal(t, "hello", be.gotOpts.Title)
	require.Empty(t, be.gotOpts.WorkingDir, "no working_dir/path: worker is unpinned")
}

// working_dir is threaded to the backend verbatim (validation against
// the target workspace happens there); a relative path is rejected up
// front so the model gets a precise error.
func TestSwarmNew_WorkingDirParam(t *testing.T) {
	t.Parallel()

	t.Run("absolute working_dir is passed through", func(t *testing.T) {
		t.Parallel()
		be := &recordingSwarmBackend{}
		resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
			Address: "new", Prompt: "hello", WorkingDir: "/proj/wt-linked2",
		})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.Equal(t, "/proj/wt-linked2", be.gotOpts.WorkingDir)
		require.Contains(t, resp.Content, "Working dir /proj/wt-linked2")
	})

	t.Run("relative working_dir is a tool error without a session", func(t *testing.T) {
		t.Parallel()
		be := &recordingSwarmBackend{}
		resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
			Address: "new", Prompt: "hello", WorkingDir: "wt-linked2",
		})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "absolute")
		require.Equal(t, session.Session{}, be.created)
	})

	t.Run("path defaults the working dir", func(t *testing.T) {
		t.Parallel()
		be := &recordingSwarmBackend{}
		resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
			Address: "new", Prompt: "hello", Path: "/proj/wt-linked2",
		})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.Equal(t, "/proj/wt-linked2", be.gotPath)
		require.Equal(t, "/proj/wt-linked2", be.created.WorkingDir)
		md := decodeSwarmMetadata(t, resp.Metadata)
		require.Equal(t, "/proj/wt-linked2", md.WorkingDir)
		require.Equal(t, "ws-at-path", md.WorkspaceID)
	})

	t.Run("working_dir on an existing address is rejected", func(t *testing.T) {
		t.Parallel()
		be := &recordingSwarmBackend{}
		resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
			Address: "plum-flamingo", Prompt: "hi", WorkingDir: "/proj",
		})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "address='new'")
	})
}

// Both success paths attach structured metadata so UIs can link to the
// target without parsing the prose.
func TestSwarm_ResultMetadata(t *testing.T) {
	t.Parallel()

	t.Run("new", func(t *testing.T) {
		t.Parallel()
		be := &recordingSwarmBackend{}
		resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
			Address: "new", Prompt: "hello", Mode: "btw",
		})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		md := decodeSwarmMetadata(t, resp.Metadata)
		require.Equal(t, SwarmResponseMetadata{
			WorkspaceID: "ws",
			SessionID:   "worker",
			Color:       "plum",
			Animal:      "flamingo",
			Address:     swarm.FormatAddress(swarm.Identity{Color: "plum", Animal: "flamingo"}, "worker"),
			Delivery:    "sent",
			BTW:         true,
			Created:     true,
		}, md)
		require.Contains(t, resp.Content, "Created and sent to")
	})

	t.Run("deliver", func(t *testing.T) {
		t.Parallel()
		be := &lookupSwarmBackend{
			target: SwarmLookupResult{WorkspaceID: "ws-other", SessionID: "target", Color: "plum", Animal: "flamingo"},
		}
		resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
			Address: "plum-flamingo", Prompt: "hi",
		})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		md := decodeSwarmMetadata(t, resp.Metadata)
		require.Equal(t, "ws-other", md.WorkspaceID)
		require.Equal(t, "target", md.SessionID)
		require.Equal(t, "queued", md.Delivery)
		require.False(t, md.Created)
		require.Empty(t, md.WorkingDir)
		require.False(t, md.BTW)
	})
}

// lookupSwarmBackend resolves every address to a fixed target and
// reports the send as queued.
type lookupSwarmBackend struct {
	stubSwarmBackend
	target SwarmLookupResult
}

func (l *lookupSwarmBackend) LookupAddress(context.Context, string) (SwarmLookupResult, error) {
	return l.target, nil
}

func (l *lookupSwarmBackend) Send(context.Context, string, SwarmLookupResult, message.SwarmMessage) (string, error) {
	return "queued", nil
}
