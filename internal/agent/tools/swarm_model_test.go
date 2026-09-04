package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// recordingSwarmBackend captures the model reference passed to the create
// paths so the swarm tool's `model` plumbing can be asserted without a
// backend.
type recordingSwarmBackend struct {
	stubSwarmBackend
	gotModelRef string
	rejectRef   bool
}

func (r *recordingSwarmBackend) CreateSessionInWorkspace(_ context.Context, _, _, modelRef string) (session.Session, error) {
	r.gotModelRef = modelRef
	if r.rejectRef && modelRef != "" {
		return session.Session{}, errors.New(`invalid session model: unknown model "` + modelRef + `"`)
	}
	return session.Session{ID: "worker", Color: "plum", Animal: "flamingo", ModelRef: modelRef}, nil
}

func (r *recordingSwarmBackend) Send(context.Context, string, SwarmLookupResult, message.SwarmMessage) (string, error) {
	return "sent", nil
}

func swarmNewCtx() context.Context {
	return context.WithValue(context.Background(), SessionIDContextKey, "sender")
}

func TestSwarmNew_ModelParamPassedToWorker(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	cfg := func() swarm.Config { return swarm.Config{} }

	resp, err := runSwarm(swarmNewCtx(), be, sess, cfg, "ws", SwarmParams{Address: "new", Prompt: "hello", Model: " scout "})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Equal(t, "scout", be.gotModelRef, "reference is passed through trimmed, resolved by the target workspace")
	require.Contains(t, resp.Content, "Runs on scout")
}

// The default is unchanged: no model -> empty ref -> the worker runs its
// workspace's large model.
func TestSwarmNew_NoModelKeepsDefault(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	cfg := func() swarm.Config { return swarm.Config{} }

	resp, err := runSwarm(swarmNewCtx(), be, sess, cfg, "ws", SwarmParams{Address: "new", Prompt: "hello"})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Empty(t, be.gotModelRef)
	require.NotContains(t, resp.Content, "Runs on")
}

// A reference the target workspace rejects is a tool error, not a hard
// error, so the model can correct it.
func TestSwarmNew_BadModelIsToolError(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{rejectRef: true}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	cfg := func() swarm.Config { return swarm.Config{} }

	resp, err := runSwarm(swarmNewCtx(), be, sess, cfg, "ws", SwarmParams{Address: "new", Prompt: "hello", Model: "nope"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "nope")
}

// `model` is rejected for existing addresses: a running session keeps its
// own model.
func TestSwarmSend_ModelRejectedForExistingSession(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	cfg := func() swarm.Config { return swarm.Config{} }

	resp, err := runSwarm(swarmNewCtx(), be, sess, cfg, "ws", SwarmParams{Address: "plum-flamingo", Prompt: "hello", Model: "scout"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "only applies with address='new'")
}
