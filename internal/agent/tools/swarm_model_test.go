package tools

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// recordingSwarmBackend captures the model passed to the create paths so
// the swarm tool's `model` plumbing can be asserted without a backend.
type recordingSwarmBackend struct {
	stubSwarmBackend
	gotModel *config.SelectedModel
	created  session.Session
}

func (r *recordingSwarmBackend) CreateSessionInWorkspace(_ context.Context, _, _ string, model *config.SelectedModel) (session.Session, error) {
	r.gotModel = model
	r.created = session.Session{ID: "worker", Color: "plum", Animal: "flamingo", Model: model}
	return r.created, nil
}

func (r *recordingSwarmBackend) Send(context.Context, string, SwarmLookupResult, message.SwarmMessage) (string, error) {
	return "sent", nil
}

func swarmModelResolver(t *testing.T) ModelResolver {
	t.Helper()
	return func(ref string) (config.SelectedModel, error) {
		switch ref {
		case "scout", "dp-claude/claude-haiku-4-5-20251001":
			return config.SelectedModel{Provider: "dp-claude", Model: "claude-haiku-4-5-20251001"}, nil
		case "shared-model":
			return config.SelectedModel{}, errors.New(`ambiguous model "shared-model": configured on providers dp-claude, dp-gpt; qualify it as <provider>/shared-model`)
		}
		return config.SelectedModel{}, errors.New(`unknown model "` + ref + `"`)
	}
}

func TestSwarmNew_ModelParamStampsWorker(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	cfg := func() swarm.Config { return swarm.Config{} }
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sender")

	resp, err := runSwarm(ctx, be, sess, cfg, "ws", swarmModelResolver(t), SwarmParams{
		Address: "new", Prompt: "hello", Model: "scout",
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.NotNil(t, be.gotModel)
	require.Equal(t, "claude-haiku-4-5-20251001", be.gotModel.Model)
	require.Contains(t, resp.Content, "Runs on dp-claude/claude-haiku-4-5-20251001")
}

func TestSwarmNew_NoModelLeavesWorkerUnstamped(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	cfg := func() swarm.Config { return swarm.Config{} }
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sender")

	resp, err := runSwarm(ctx, be, sess, cfg, "ws", swarmModelResolver(t), SwarmParams{Address: "new", Prompt: "hello"})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Nil(t, be.gotModel, "worker must resolve to large when no model is given")
	require.NotContains(t, resp.Content, "Runs on")
}

// An ambiguous or unknown model is a tool error (so the model can fix
// it) and no session is created.
func TestSwarmNew_BadModelIsToolErrorWithoutSession(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"shared-model", "nope"} {
		be := &recordingSwarmBackend{}
		sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
		cfg := func() swarm.Config { return swarm.Config{} }
		ctx := context.WithValue(context.Background(), SessionIDContextKey, "sender")

		resp, err := runSwarm(ctx, be, sess, cfg, "ws", swarmModelResolver(t), SwarmParams{Address: "new", Prompt: "hello", Model: ref})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Equal(t, session.Session{}, be.created, "no session may be created for %q", ref)
	}
	// Ambiguity message tells the caller how to qualify.
	be := &recordingSwarmBackend{}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sender")
	resp, _ := runSwarm(ctx, be, sess, func() swarm.Config { return swarm.Config{} }, "ws", swarmModelResolver(t), SwarmParams{Address: "new", Prompt: "hello", Model: "shared-model"})
	require.Contains(t, resp.Content, "<provider>/shared-model")
}

// `model` on an existing address is rejected rather than silently
// ignored: the target keeps its own model.
func TestSwarmDeliver_ModelParamRejected(t *testing.T) {
	t.Parallel()
	be := &recordingSwarmBackend{}
	sess := stubSessionsForSwarm{sess: session.Session{ID: "sender", Color: "aliceblue", Animal: "tiger"}}
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sender")
	resp, err := runSwarm(ctx, be, sess, func() swarm.Config { return swarm.Config{} }, "ws", swarmModelResolver(t), SwarmParams{Address: "plum-flamingo", Prompt: "hi", Model: "scout"})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "address='new'")
}
