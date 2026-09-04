package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// replyBackend records forwarded replies so the fallback path can be
// asserted without a real backend.
type replyBackend struct {
	sends []message.SwarmMessage
	to    []string
}

func (b *replyBackend) LookupAddress(_ context.Context, addr string) (tools.SwarmLookupResult, error) {
	return tools.SwarmLookupResult{WorkspaceID: "ws", SessionID: addr}, nil
}

func (b *replyBackend) Send(_ context.Context, _ string, target tools.SwarmLookupResult, part message.SwarmMessage) (string, error) {
	b.sends = append(b.sends, part)
	b.to = append(b.to, target.SessionID)
	return "sent", nil
}

func (*replyBackend) CreateSessionInWorkspace(context.Context, string, tools.SwarmNewOptions) (session.Session, error) {
	return session.Session{}, errors.New("unused")
}

func (*replyBackend) ArchiveSessionInWorkspace(context.Context, string, string) error { return nil }
func (*replyBackend) CreateSessionInWorkspaceAtPath(context.Context, string, tools.SwarmNewOptions) (string, session.Session, error) {
	return "", session.Session{}, errors.New("unused")
}

func (*replyBackend) ResolveWorkspaceByPath(context.Context, string) (string, bool, error) {
	return "", false, nil
}

func (*replyBackend) RenameSession(context.Context, tools.SwarmLookupResult, string) error {
	return nil
}

type replySessions struct {
	session.Service
}

func (replySessions) Get(_ context.Context, id string) (session.Session, error) {
	return session.Session{ID: id, Color: "plum", Animal: "flamingo"}, nil
}

func newReplyCoordinator(be *replyBackend) *coordinator {
	return &coordinator{
		sessions:         replySessions{},
		swarmReplies:     swarm.NewReplyTracker(),
		swarmBackend:     be,
		swarmWorkspaceID: "ws",
	}
}

func textResult(text string) *fantasy.AgentResult {
	return &fantasy.AgentResult{Response: fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: text}}}}
}

func TestRegisterReplyObligations_OnlyRequireReplyParts(t *testing.T) {
	t.Parallel()
	c := newReplyCoordinator(&replyBackend{})
	c.registerReplyObligations("worker", []message.SwarmMessage{
		{SenderSessionID: "parent", SenderColor: "red", SenderAnimal: "fox", Body: "do it", RequireReply: true},
		{SenderSessionID: "bystander", Body: "fyi"},
		{RequireReply: true},
	})
	got := c.swarmReplies.Pending("worker")
	require.Len(t, got, 1)
	require.Equal(t, "parent", got[0].SenderSessionID)
	require.Equal(t, swarm.FormatAddress(swarm.Identity{Color: "red", Animal: "fox"}, "parent"), got[0].SenderAddress)
	require.Equal(t, "do it", got[0].Body)
}

func TestAdvanceReplyObligations_NoneOwed(t *testing.T) {
	t.Parallel()
	c := newReplyCoordinator(&replyBackend{})
	prompt, ok := c.advanceReplyObligations(context.Background(), "worker", textResult("done"))
	require.False(t, ok)
	require.Empty(t, prompt)
}

func TestAdvanceReplyObligations_NudgesThenForwards(t *testing.T) {
	t.Parallel()
	be := &replyBackend{}
	c := newReplyCoordinator(be)
	c.registerReplyObligations("worker", []message.SwarmMessage{
		{SenderSessionID: "parent", SenderColor: "red", SenderAnimal: "fox", Body: "refactor foo\nsecond line", RequireReply: true},
	})
	ctx := context.Background()

	for i := 1; i <= swarmReplyMaxNudges; i++ {
		prompt, ok := c.advanceReplyObligations(ctx, "worker", textResult("I did the thing."))
		require.True(t, ok, "nudge %d should continue the turn", i)
		require.Contains(t, prompt, "[reply required]")
		require.Contains(t, prompt, `address="red-fox-`)
		require.Contains(t, prompt, "they asked: refactor foo")
		require.NotContains(t, prompt, "second line")
		require.Empty(t, be.sends, "no fallback while nudges remain")
	}

	prompt, ok := c.advanceReplyObligations(ctx, "worker", textResult("I did the thing."))
	require.False(t, ok, "budget spent: turn may end")
	require.Empty(t, prompt)
	require.Len(t, be.sends, 1)
	require.Equal(t, []string{"parent"}, be.to)
	require.Contains(t, be.sends[0].Body, "[auto-forwarded:")
	require.Contains(t, be.sends[0].Body, "I did the thing.")
	require.False(t, be.sends[0].RequireReply)
	require.Equal(t, "worker", be.sends[0].SenderSessionID)
	require.Empty(t, c.swarmReplies.Pending("worker"))
}

func TestAdvanceReplyObligations_FulfilledMidway(t *testing.T) {
	t.Parallel()
	be := &replyBackend{}
	c := newReplyCoordinator(be)
	c.registerReplyObligations("worker", []message.SwarmMessage{
		{SenderSessionID: "parent", RequireReply: true},
	})
	_, ok := c.advanceReplyObligations(context.Background(), "worker", textResult("x"))
	require.True(t, ok)

	// The agent replies during the nudge turn.
	require.True(t, c.swarmReplies.Fulfill("worker", "parent"))

	_, ok = c.advanceReplyObligations(context.Background(), "worker", textResult("sent"))
	require.False(t, ok)
	require.Empty(t, be.sends)
}

func TestFailReplyObligations_ForwardsErrorAndClears(t *testing.T) {
	t.Parallel()
	be := &replyBackend{}
	c := newReplyCoordinator(be)
	c.registerReplyObligations("worker", []message.SwarmMessage{
		{SenderSessionID: "parent", RequireReply: true},
		{SenderSessionID: "aunt", RequireReply: true},
	})

	c.failReplyObligations("worker", errors.New("provider exploded"))
	require.Len(t, be.sends, 2)
	require.ElementsMatch(t, []string{"parent", "aunt"}, be.to)
	require.Contains(t, be.sends[0].Body, "the turn failed: provider exploded")
	require.Empty(t, c.swarmReplies.Pending("worker"))
}

func TestFailReplyObligations_Canceled(t *testing.T) {
	t.Parallel()
	be := &replyBackend{}
	c := newReplyCoordinator(be)
	c.registerReplyObligations("worker", []message.SwarmMessage{{SenderSessionID: "parent", RequireReply: true}})
	c.failReplyObligations("worker", context.Canceled)
	require.Len(t, be.sends, 1)
	require.Contains(t, be.sends[0].Body, "the turn was canceled")
}

func TestFailReplyObligations_NothingOwedIsNoop(t *testing.T) {
	t.Parallel()
	be := &replyBackend{}
	c := newReplyCoordinator(be)
	c.failReplyObligations("worker", errors.New("boom"))
	require.Empty(t, be.sends)
}

func TestReplyOnBehalf_NoBackendDropsQuietly(t *testing.T) {
	t.Parallel()
	c := &coordinator{swarmReplies: swarm.NewReplyTracker(), sessions: replySessions{}}
	c.replyOnBehalf("worker", swarm.ReplyObligation{SenderSessionID: "parent"}, "x")
}

func TestDropReplyObligations_ReleasesOnlyDroppedSenders(t *testing.T) {
	t.Parallel()
	be := &replyBackend{}
	c := newReplyCoordinator(be)
	parts := []message.SwarmMessage{
		{SenderSessionID: "parent", SenderColor: "red", SenderAnimal: "fox", Body: "do it", RequireReply: true},
	}
	c.registerReplyObligations("worker", parts)
	c.registerReplyObligations("worker", []message.SwarmMessage{{SenderSessionID: "aunt", RequireReply: true}})

	// The parent's queued message is discarded before it runs.
	c.dropReplyObligations(SessionAgentCall{SessionID: "worker", SwarmParts: parts})

	require.Len(t, be.sends, 1)
	require.Equal(t, []string{"parent"}, be.to)
	require.Contains(t, be.sends[0].Body, "discarded before it ran")
	pending := c.swarmReplies.Pending("worker")
	require.Len(t, pending, 1, "the aunt's live obligation must survive")
	require.Equal(t, "aunt", pending[0].SenderSessionID)

	// Dropping a call whose obligation is already gone is a no-op.
	c.dropReplyObligations(SessionAgentCall{SessionID: "worker", SwarmParts: parts})
	require.Len(t, be.sends, 1)
}

// TestDeferPrompt_RegistersUndeliveredObligation: a swarm message parked
// via DeferPrompt (drain-time delivery, or a replayed queue tail) never
// passes through coordinator.run, so DeferPrompt records the reply its
// sender demanded — but as undelivered: the agent has not seen the
// message, so the current turn ending must neither nudge for it nor
// report it failed. Once the queued call is dispatched the obligation
// becomes enforceable.
func TestDeferPrompt_RegistersUndeliveredObligation(t *testing.T) {
	t.Parallel()
	be := &replyBackend{}
	c := newReplyCoordinator(be)
	c.currentAgent = &mockSessionAgent{}
	parts := []message.SwarmMessage{
		{SenderSessionID: "parent", SenderColor: "red", SenderAnimal: "fox", Body: "do it", RequireReply: true},
	}
	c.DeferPrompt("worker", "run-1", "message from parent: do it", nil, parts)
	got := c.swarmReplies.Pending("worker")
	require.Len(t, got, 1)
	require.Equal(t, "parent", got[0].SenderSessionID)
	require.True(t, got[0].Undelivered)

	// The active turn (which never saw the message) ends: no nudge, and
	// a failure does not blame the undelivered message either.
	_, ok := c.advanceReplyObligations(t.Context(), "worker", textResult("done"))
	require.False(t, ok, "no nudge turn for a message the agent has not received")
	c.failReplyObligations("worker", errors.New("boom"))
	require.Empty(t, be.sends, "the sender must not be told the work failed")
	require.Len(t, c.swarmReplies.Pending("worker"), 1, "the obligation survives for when the message runs")

	// The queued call is dispatched: now it is enforced.
	c.deliverReplyObligations(SessionAgentCall{SessionID: "worker", SwarmParts: parts})
	require.False(t, c.swarmReplies.Pending("worker")[0].Undelivered)
	prompt, ok := c.advanceReplyObligations(t.Context(), "worker", textResult("done"))
	require.True(t, ok)
	require.Contains(t, prompt, "reply required")
}
