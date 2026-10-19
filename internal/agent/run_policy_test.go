package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/fantasy"
)

// policyRecordingModel records the run permission policy visible on the
// context of every Stream call, which is the context tools run under.
type policyRecordingModel struct {
	*gatedStreamModel
	mu       sync.Mutex
	policies []permission.SessionPolicy
}

func (m *policyRecordingModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	m.mu.Lock()
	m.policies = append(m.policies, permission.PolicyFromContext(ctx))
	m.mu.Unlock()
	return m.gatedStreamModel.Stream(ctx, call)
}

func (m *policyRecordingModel) seen() []permission.SessionPolicy {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]permission.SessionPolicy(nil), m.policies...)
}

// TestRun_PermissionPolicyIsPerTurn proves a headless run's permission
// policy governs only its own turn: a turn queued behind another runs
// under its own policy, never the one of the turn that dispatches it,
// in both directions.
func TestRun_PermissionPolicyIsPerTurn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		main, follow permission.SessionPolicy
	}{
		{"headless turn queued behind a TUI turn", "", permission.SessionPolicyAllow},
		{"TUI turn queued behind a headless turn", permission.SessionPolicyDeny, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := testEnv(t)
			broker := pubsub.NewBroker[notify.RunComplete]()
			t.Cleanup(broker.Shutdown)

			large := &policyRecordingModel{gatedStreamModel: &gatedStreamModel{
				text: "done", gate: make(chan struct{}), entered: make(chan struct{}),
			}}
			sa := NewSessionAgent(SessionAgentOptions{
				LargeModel:  Model{Model: large, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
				SmallModel:  Model{Model: &finishStreamModel{text: "title"}, CatwalkCfg: catwalk.Model{ContextWindow: 200000, DefaultMaxTokens: 10000}},
				IsYolo:      true,
				Sessions:    env.sessions,
				Messages:    env.messages,
				RunComplete: broker,
			}).(*sessionAgent)

			sess, err := env.sessions.Create(t.Context(), "session")
			require.NoError(t, err)

			// The parent context already carries a policy, as a turn
			// dispatched from inside another turn's stack would.
			parent := permission.WithPolicy(t.Context(), permission.SessionPolicyAllow)

			mainDone := make(chan error, 1)
			go func() {
				_, runErr := sa.Run(parent, SessionAgentCall{
					SessionID: sess.ID, RunID: "main", Prompt: "main", PermissionPolicy: tc.main,
				})
				mainDone <- runErr
			}()
			select {
			case <-large.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("main run never entered Stream")
			}

			_, err = sa.Run(parent, SessionAgentCall{
				SessionID: sess.ID, RunID: "follow", Prompt: "follow", PermissionPolicy: tc.follow,
			})
			require.NoError(t, err)
			require.Equal(t, 1, sa.QueuedPrompts(sess.ID))

			close(large.gate)
			require.NoError(t, <-mainDone)

			seen := large.seen()
			require.GreaterOrEqual(t, len(seen), 2)
			require.Equal(t, tc.main, seen[0], "the first turn runs under its own policy")
			require.Equal(t, tc.follow, seen[len(seen)-1], "the queued turn runs under its own policy")
		})
	}
}
