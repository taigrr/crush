package agent

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/agent/notify"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/pubsub"
)

type busyAtPublish struct {
	busy     func() bool
	observed []bool
}

func (p *busyAtPublish) Publish(pubsub.EventType, notify.RunComplete) {
	p.observed = append(p.observed, p.busy())
}

func (p *busyAtPublish) PublishMustDeliver(context.Context, pubsub.EventType, notify.RunComplete) {
	p.observed = append(p.observed, p.busy())
}

func TestCoordinatorRun_BusyBetweenAgentTurns(t *testing.T) {
	t.Run("goal evaluation between turns reports busy", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		c.cfg.Config().Agents = map[string]config.Agent{config.AgentCoder: {ID: config.AgentCoder, AllowedTools: []string{}}}
		sess, err := env.sessions.Create(t.Context(), "Goal")
		require.NoError(t, err)

		var busyDuringAdvance []bool
		runs := 0
		mock := c.currentAgent.(*mockSessionAgent)
		mock.runFunc = func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
			runs++
			return agentResultWithText("done"), nil
		}
		mock.advance = func(context.Context, string) (bool, string) {
			busyDuringAdvance = append(busyDuringAdvance, c.IsSessionBusy(sess.ID), c.IsBusy())
			return runs < 2, "keep going"
		}

		require.False(t, c.IsSessionBusy(sess.ID))
		_, err = c.run(t.Context(), nil, sess.ID, "work")
		require.NoError(t, err)
		require.Equal(t, 2, runs)
		require.Equal(t, []bool{true, true, true, true}, busyDuringAdvance)
		require.False(t, c.IsSessionBusy(sess.ID), "the hold is released when the run ends")
		require.False(t, c.IsBusy())
	})

	t.Run("unauthorized retry reports busy", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		c.cfg.Config().Agents = map[string]config.Agent{config.AgentCoder: {ID: config.AgentCoder, AllowedTools: []string{}}}
		sess, err := env.sessions.Create(t.Context(), "Retry")
		require.NoError(t, err)

		var busyOnRetry bool
		runs := 0
		mock := c.currentAgent.(*mockSessionAgent)
		mock.runFunc = func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error) {
			runs++
			if runs == 1 {
				return nil, &fantasy.ProviderError{StatusCode: http.StatusUnauthorized}
			}
			busyOnRetry = c.IsSessionBusy(sess.ID)
			return agentResultWithText("done"), nil
		}

		_, err = c.run(t.Context(), nil, sess.ID, "work")
		require.NoError(t, err)
		require.Equal(t, 2, runs)
		require.True(t, busyOnRetry, "the session stays busy across the retry")
		require.False(t, c.IsSessionBusy(sess.ID))
	})

	t.Run("hold is released before the terminal event", func(t *testing.T) {
		env := testEnv(t)
		c := roleCoordinator(t, env)
		c.cfg.Config().Agents = map[string]config.Agent{config.AgentCoder: {ID: config.AgentCoder, AllowedTools: []string{}}}
		sess, err := env.sessions.Create(t.Context(), "Terminal")
		require.NoError(t, err)

		pub := &busyAtPublish{busy: func() bool { return c.IsSessionBusy(sess.ID) }}
		c.runComplete = pub
		mock := c.currentAgent.(*mockSessionAgent)
		mock.runFunc = func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			call.OnComplete(notify.RunComplete{SessionID: call.SessionID})
			return agentResultWithText("done"), nil
		}

		_, err = c.run(t.Context(), nil, sess.ID, "work")
		require.NoError(t, err)
		require.Equal(t, []bool{false}, pub.observed, "observers reacting to RunComplete must see the session idle")
	})
}

func TestCoordinatorHoldTurn_PerSessionAndNested(t *testing.T) {
	t.Parallel()
	c := &coordinator{currentAgent: &mockSessionAgent{}}
	first := c.holdTurn("a")
	second := c.holdTurn("a")
	require.True(t, c.IsSessionBusy("a"))
	require.False(t, c.IsSessionBusy("b"))
	first()
	first()
	require.True(t, c.IsSessionBusy("a"), "a double release never drops another run's hold")
	second()
	require.False(t, c.IsSessionBusy("a"))
	require.False(t, c.IsBusy())
}
