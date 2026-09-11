package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// partRecordingBackend captures the part handed to Send and resolves
// every address to a fixed target.
type partRecordingBackend struct {
	stubSwarmBackend
	target  SwarmLookupResult
	gotPart message.SwarmMessage
	gotFrom string
	sends   int
}

func (b *partRecordingBackend) LookupAddress(context.Context, string) (SwarmLookupResult, error) {
	return b.target, nil
}

func (b *partRecordingBackend) Send(_ context.Context, from string, _ SwarmLookupResult, part message.SwarmMessage) (string, error) {
	b.gotFrom = from
	b.gotPart = part
	b.sends++
	return "sent", nil
}

func (b *partRecordingBackend) CreateSessionInWorkspace(context.Context, string, SwarmNewOptions) (session.Session, error) {
	return session.Session{ID: "worker", Color: "plum", Animal: "flamingo"}, nil
}

func TestSwarmRequireReply_StampsPartAndTrailer(t *testing.T) {
	t.Parallel()
	be := &partRecordingBackend{target: SwarmLookupResult{WorkspaceID: "ws", SessionID: "worker", Color: "plum", Animal: "flamingo"}}
	resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
		Address: "plum-flamingo", Prompt: "do the thing", RequireReply: true,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	require.True(t, be.gotPart.RequireReply)
	require.Equal(t, "do the thing", be.gotPart.Body, "body stays un-prefixed")
	senderAddr := swarm.FormatAddress(swarm.Identity{Color: "aliceblue", Animal: "tiger"}, "sender")
	require.Contains(t, be.gotPart.Text, "message from aliceblue-tiger: do the thing")
	require.Contains(t, be.gotPart.Text, ReplyRequiredTrailer(senderAddr))

	md := decodeSwarmMetadata(t, resp.Metadata)
	require.True(t, md.ReplyRequired)
	require.False(t, md.FulfilledReply)
	require.Contains(t, resp.Content, "Reply required")
}

func TestSwarmRequireReply_OnNew(t *testing.T) {
	t.Parallel()
	be := &partRecordingBackend{}
	resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
		Address: "new", Prompt: "build it", RequireReply: true,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.True(t, be.gotPart.RequireReply)
	require.Contains(t, be.gotPart.Text, "[reply required:")
	require.True(t, decodeSwarmMetadata(t, resp.Metadata).ReplyRequired)
}

func TestSwarm_NoRequireReply_NoTrailer(t *testing.T) {
	t.Parallel()
	be := &partRecordingBackend{target: SwarmLookupResult{WorkspaceID: "ws", SessionID: "worker"}}
	resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", nil, SwarmParams{
		Address: "worker", Prompt: "hi",
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.False(t, be.gotPart.RequireReply)
	require.Equal(t, "message from aliceblue-tiger: hi", be.gotPart.Text)
	require.False(t, decodeSwarmMetadata(t, resp.Metadata).ReplyRequired)
}

// Sending to a session we owe a reply to fulfills the obligation,
// regardless of mode, and says so in the result.
func TestSwarmDeliver_FulfillsOwedReply(t *testing.T) {
	t.Parallel()
	replies := swarm.NewReplyTracker()
	replies.Require("sender", swarm.ReplyObligation{SenderSessionID: "parent", SenderAddress: "red-fox-1234"})
	replies.Require("sender", swarm.ReplyObligation{SenderSessionID: "other"})

	be := &partRecordingBackend{target: SwarmLookupResult{WorkspaceID: "ws", SessionID: "parent", Color: "red", Animal: "fox"}}
	resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", replies, SwarmParams{
		Address: "red-fox", Prompt: "done: all tests pass", Mode: "btw",
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.True(t, decodeSwarmMetadata(t, resp.Metadata).FulfilledReply)
	require.Contains(t, resp.Content, "satisfies the reply you owed")

	pending := replies.Pending("sender")
	require.Len(t, pending, 1)
	require.Equal(t, "other", pending[0].SenderSessionID)
}

func TestSwarmDeliver_UnrelatedSendDoesNotFulfill(t *testing.T) {
	t.Parallel()
	replies := swarm.NewReplyTracker()
	replies.Require("sender", swarm.ReplyObligation{SenderSessionID: "parent"})

	be := &partRecordingBackend{target: SwarmLookupResult{WorkspaceID: "ws", SessionID: "stranger"}}
	resp, err := runSwarm(swarmSenderCtx(), be, swarmSenderSessions(), swarm.Default, "ws", replies, SwarmParams{
		Address: "stranger", Prompt: "hello",
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.False(t, decodeSwarmMetadata(t, resp.Metadata).FulfilledReply)
	require.Len(t, replies.Pending("sender"), 1)
}

func TestSwarmReplyOnBehalf(t *testing.T) {
	t.Parallel()
	be := &partRecordingBackend{target: SwarmLookupResult{WorkspaceID: "ws", SessionID: "parent", Color: "red", Animal: "fox"}}
	err := SwarmReplyOnBehalf(context.Background(), be, swarmSenderSessions(), nil, "ws", "sender", "parent", "final words")
	require.NoError(t, err)
	require.Equal(t, 1, be.sends)
	require.Equal(t, "sender", be.gotFrom)
	require.Equal(t, "sender", be.gotPart.SenderSessionID)
	require.Equal(t, "aliceblue", be.gotPart.SenderColor)
	require.Equal(t, "final words", be.gotPart.Body)
	require.Equal(t, "message from aliceblue-tiger: final words", be.gotPart.Text)
	require.False(t, be.gotPart.RequireReply, "a forwarded reply must never demand a reply back")
	require.False(t, be.gotPart.BTW)
}

func TestSwarmReplyOnBehalf_RefusesSelf(t *testing.T) {
	t.Parallel()
	be := &partRecordingBackend{target: SwarmLookupResult{WorkspaceID: "ws", SessionID: "sender"}}
	err := SwarmReplyOnBehalf(context.Background(), be, swarmSenderSessions(), nil, "ws", "sender", "sender", "x")
	require.Error(t, err)
	require.Zero(t, be.sends)
}
