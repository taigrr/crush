package cmd

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/headless/headlesstest"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/proto"
)

// These tests drive the headless commands end to end against a real
// in-process server: real HTTP, SSE, backend, permission service, and
// SQLite stores, with only the LLM turn scripted.

func e2eRun(t *testing.T, h *headlesstest.Harness, sess *proto.Session, opts runOptions) (map[string]any, error) {
	t.Helper()
	var out bytes.Buffer
	if opts.format == "" {
		opts.format = headless.FormatJSON
	}
	if opts.mode == "" {
		opts.mode = headless.ModeYOLO
	}
	opts.prompt = "please do the thing"
	err := runHeadless(t.Context(), h.Client, h.Workspace, sess, opts, &out)
	lines := decodeLines(t, &out)
	require.NotEmpty(t, lines)
	return lines[len(lines)-1], err
}

func TestE2E_DelegatedRunBlocksThenAnswerResumes(t *testing.T) {
	h := headlesstest.New(t)
	h.Agent.AskPermission = true
	h.Agent.Reply = "ran echo"

	sess, err := h.Client.CreateSession(t.Context(), h.Workspace.ID, "e2e")
	require.NoError(t, err)
	require.NotEmpty(t, sess.Color, "new sessions get their swarm identity right away")

	res, err := e2eRun(t, h, sess, runOptions{mode: headless.ModeDelegate})
	require.Equal(t, headless.ExitBlocked, headless.ExitCode(err), "%v", res)
	require.Equal(t, "blocked", res["status"])
	require.Equal(t, headless.SessionAddress(*sess), res["address"])
	perms := res["pending"].(map[string]any)["permissions"].([]any)
	require.Len(t, perms, 1)
	require.Equal(t, "echo hello", perms[0].(map[string]any)["description"])

	// A second process (a fresh client) finds the blocked run by its
	// swarm address and sees it waiting.
	other := h.NewClient(t)
	resolved, err := resolveSessionRef(t.Context(), other, h.Workspace.ID, headless.SessionAddress(*sess))
	require.NoError(t, err)
	require.Equal(t, sess.ID, resolved.ID)

	var status bytes.Buffer
	require.NoError(t, sessionStatus(t.Context(), other, h.Workspace, resolved, headless.FormatJSON, &status))
	st := decodeLines(t, &status)[0]
	require.Equal(t, "blocked", st["state"])

	var answered bytes.Buffer
	err = sessionAnswer(t.Context(), other, h.Workspace, resolved,
		headless.AnswerRequest{Permission: proto.PermissionAllow}, true, 10*time.Second, headless.FormatJSON, &answered)
	require.NoError(t, err)
	final := decodeLines(t, &answered)[0]
	require.Equal(t, "completed", final["status"])
	require.Equal(t, "ran echo", final["text"])

	h.WaitIdle(t)
	require.Equal(t, []permission.SessionPolicy{permission.SessionPolicyPrompt}, h.Agent.Policies())
	require.Equal(t, permission.SessionPolicyPrompt, h.Backend.Permissions.SessionPolicy(sess.ID))

	status.Reset()
	require.NoError(t, sessionStatus(t.Context(), other, h.Workspace, resolved, headless.FormatJSON, &status))
	st = decodeLines(t, &status)[0]
	require.Equal(t, "idle", st["state"])
	require.Equal(t, "ran echo", st["last"].(map[string]any)["text"])
}

func TestE2E_DenyModeStopsOnFirstPermission(t *testing.T) {
	h := headlesstest.New(t)
	h.Agent.AskPermission = true

	sess, err := h.Client.CreateSession(t.Context(), h.Workspace.ID, "e2e")
	require.NoError(t, err)

	res, err := e2eRun(t, h, sess, runOptions{mode: headless.ModeDeny})
	require.Equal(t, headless.ExitPermissionDenied, headless.ExitCode(err), "%v", res)
	require.Equal(t, "permission_denied", res["status"])
	require.Equal(t, "Running echo", res["text"], "a denial ends the turn at the tool call")

	h.WaitIdle(t)
	require.Equal(t, []permission.SessionPolicy{permission.SessionPolicyDeny}, h.Agent.Policies())
	require.Equal(t, permission.SessionPolicyPrompt, h.Backend.Permissions.SessionPolicy(sess.ID),
		"run policies never change the session default")
}

func TestE2E_YOLOModeApprovesAndReportsUsage(t *testing.T) {
	h := headlesstest.New(t)
	h.Agent.AskPermission = true
	h.Agent.Reply = "approved automatically"

	sess, err := h.Client.CreateSession(t.Context(), h.Workspace.ID, "e2e")
	require.NoError(t, err)

	res, err := e2eRun(t, h, sess, runOptions{format: headless.FormatStreamJSON})
	require.NoError(t, err)
	require.Equal(t, "completed", res["status"])
	require.Equal(t, "approved automatically", res["text"])
	require.Equal(t, []any{}, res["files_changed"])
	require.NotNil(t, res["usage"])
	require.Equal(t, []permission.SessionPolicy{permission.SessionPolicyAllow}, h.Agent.Policies())
}

func TestE2E_DetachThenWait(t *testing.T) {
	h := headlesstest.New(t)
	h.Agent.Hold = make(chan struct{})
	h.Agent.Reply = "background work finished"

	sess, err := h.Client.CreateSession(t.Context(), h.Workspace.ID, "e2e")
	require.NoError(t, err)

	res, err := e2eRun(t, h, sess, runOptions{detach: true})
	require.NoError(t, err)
	require.Equal(t, "running", res["status"])
	require.Equal(t, sess.ID, res["session_id"])

	other := h.NewClient(t)
	var waited bytes.Buffer
	err = sessionWait(t.Context(), other, h.Workspace, sess, 50*time.Millisecond, headless.FormatJSON, &waited)
	require.Equal(t, headless.ExitTimeout, headless.ExitCode(err))
	require.Equal(t, "timeout", decodeLines(t, &waited)[0]["status"])
	require.True(t, h.Agent.IsSessionBusy(sess.ID), "timing out a wait never stops the run")

	close(h.Agent.Hold)
	waited.Reset()
	require.NoError(t, sessionWait(t.Context(), other, h.Workspace, sess, 10*time.Second, headless.FormatJSON, &waited))
	final := decodeLines(t, &waited)[0]
	require.Equal(t, "completed", final["status"])
	require.Equal(t, "background work finished", final["text"])
}

func TestE2E_CancelStopsARun(t *testing.T) {
	h := headlesstest.New(t)
	h.Agent.Hold = make(chan struct{})
	t.Cleanup(func() { close(h.Agent.Hold) })

	sess, err := h.Client.CreateSession(t.Context(), h.Workspace.ID, "e2e")
	require.NoError(t, err)
	_, err = e2eRun(t, h, sess, runOptions{detach: true})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return h.Agent.IsSessionBusy(sess.ID) }, 5*time.Second, 10*time.Millisecond)

	other := h.NewClient(t)
	var out bytes.Buffer
	require.NoError(t, sessionCancel(t.Context(), other, h.Workspace, sess, headless.FormatJSON, &out))
	require.Equal(t, true, decodeLines(t, &out)[0]["was_busy"])

	out.Reset()
	err = sessionWait(t.Context(), other, h.Workspace, sess, 10*time.Second, headless.FormatJSON, &out)
	require.Equal(t, headless.ExitCancelled, headless.ExitCode(err))
	require.Equal(t, "cancelled", decodeLines(t, &out)[0]["status"])
}
