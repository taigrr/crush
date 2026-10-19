package permission

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func policyRequest(sessionID, toolCallID string) CreatePermissionRequest {
	return CreatePermissionRequest{
		SessionID:  sessionID,
		ToolCallID: toolCallID,
		ToolName:   "bash",
		Action:     "execute",
		Path:       "/tmp",
	}
}

func TestParseSessionPolicy(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]SessionPolicy{
		"":       SessionPolicyPrompt,
		"prompt": SessionPolicyPrompt,
		"allow":  SessionPolicyAllow,
		"deny":   SessionPolicyDeny,
	} {
		got, err := ParseSessionPolicy(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
	_, err := ParseSessionPolicy("yolo")
	require.ErrorContains(t, err, `invalid session permission policy "yolo"`)
}

func TestSessionPolicy_DefaultsAndReset(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil)
	require.Equal(t, SessionPolicyPrompt, svc.SessionPolicy("s"))

	svc.SetSessionPolicy("s", SessionPolicyDeny)
	require.Equal(t, SessionPolicyDeny, svc.SessionPolicy("s"))
	require.Equal(t, SessionPolicyPrompt, svc.SessionPolicy("other"), "policies are per session")

	svc.SetSessionPolicy("s", SessionPolicyPrompt)
	require.Equal(t, SessionPolicyPrompt, svc.SessionPolicy("s"))

	svc.AutoApproveSession("s")
	require.Equal(t, SessionPolicyAllow, svc.SessionPolicy("s"), "AutoApproveSession is the allow policy")

	svc.SetSessionPolicy("s", "")
	require.Equal(t, SessionPolicyPrompt, svc.SessionPolicy("s"), "empty clears the override")
}

func TestSessionPolicy_AllowGrantsWithoutPublishing(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil)
	svc.SetSessionPolicy("s", SessionPolicyAllow)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	granted, err := svc.Request(ctx, policyRequest("s", "tc1"))
	require.NoError(t, err)
	require.True(t, granted)
	require.Empty(t, svc.PendingRequests("s"))
}

func TestSessionPolicy_DenyRejectsAndNotifies(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil)
	svc.SetSessionPolicy("s", SessionPolicyDeny)

	notes := svc.SubscribeNotifications(t.Context())

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	granted, err := svc.Request(ctx, policyRequest("s", "tc1"))
	require.NoError(t, err)
	require.False(t, granted)

	select {
	case ev := <-notes:
		require.Equal(t, "s", ev.Payload.SessionID)
		require.Equal(t, "tc1", ev.Payload.ToolCallID)
		require.True(t, ev.Payload.Denied)
		require.False(t, ev.Payload.Granted)
	case <-time.After(time.Second):
		t.Fatal("deny policy must publish a denial notification")
	}
}

func TestSessionPolicy_DenyWinsOverWorkspaceSkip(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", true, nil)
	svc.SetSessionPolicy("unattended", SessionPolicyDeny)

	granted, err := svc.Request(t.Context(), policyRequest("unattended", "tc1"))
	require.NoError(t, err)
	require.False(t, granted, "enabling yolo for the workspace must not escalate a deny-policy run")

	granted, err = svc.Request(t.Context(), policyRequest("interactive", "tc2"))
	require.NoError(t, err)
	require.True(t, granted, "other sessions still honor the workspace skip toggle")
}

func TestSessionPolicy_DenyHonorsAllowlistAndHooks(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, []string{"view"})
	svc.SetSessionPolicy("s", SessionPolicyDeny)

	viewReq := policyRequest("s", "tc1")
	viewReq.ToolName = "view"
	granted, err := svc.Request(t.Context(), viewReq)
	require.NoError(t, err)
	require.True(t, granted, "allowed_tools still pre-approve under deny")

	hookCtx := WithHookApproval(t.Context(), "tc2")
	granted, err = svc.Request(hookCtx, policyRequest("s", "tc2"))
	require.NoError(t, err)
	require.True(t, granted, "a PreToolUse hook approval still pre-approves under deny")

	granted, err = svc.Request(t.Context(), policyRequest("s", "tc3"))
	require.NoError(t, err)
	require.False(t, granted)
}

func TestPendingRequests(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil)
	requests := svc.Subscribe(t.Context())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type result struct {
		granted bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		granted, err := svc.Request(ctx, policyRequest("s", "tc1"))
		done <- result{granted, err}
	}()

	var req PermissionRequest
	select {
	case ev := <-requests:
		req = ev.Payload
	case <-time.After(time.Second):
		t.Fatal("request was never published")
	}

	pending := svc.PendingRequests("s")
	require.Len(t, pending, 1)
	require.Equal(t, req.ID, pending[0].ID)
	require.Equal(t, "tc1", pending[0].ToolCallID)
	require.Empty(t, svc.PendingRequests("other"))

	require.True(t, svc.Grant(pending[0]))
	select {
	case r := <-done:
		require.NoError(t, r.err)
		require.True(t, r.granted)
	case <-time.After(time.Second):
		t.Fatal("grant did not unblock the request")
	}
	require.Empty(t, svc.PendingRequests("s"))
}

func TestContextPolicy(t *testing.T) {
	t.Parallel()

	require.Equal(t, SessionPolicy(""), PolicyFromContext(t.Context()))
	ctx := WithPolicy(t.Context(), SessionPolicyDeny)
	require.Equal(t, SessionPolicyDeny, PolicyFromContext(ctx))
	require.Equal(t, SessionPolicy(""), PolicyFromContext(WithPolicy(ctx, "")),
		"an empty policy masks one inherited from a parent context")

	svc := NewPermissionService("/tmp", false, nil)
	svc.AutoApproveSession("s")
	p, explicit := EffectivePolicy(t.Context(), svc, "s")
	require.Equal(t, SessionPolicyAllow, p)
	require.False(t, explicit)
	p, explicit = EffectivePolicy(ctx, svc, "s")
	require.Equal(t, SessionPolicyDeny, p, "the run's policy wins over the session default")
	require.True(t, explicit)
}

func TestContextPolicy_DenyWinsOverSessionAllow(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil)
	svc.AutoApproveSession("fetch-child")
	granted, err := svc.Request(WithPolicy(t.Context(), SessionPolicyDeny), policyRequest("fetch-child", "tc"))
	require.NoError(t, err)
	require.False(t, granted, "a deny run's sub-agents stay denied even if they auto-approve themselves")
}

func TestContextPolicy_ExplicitPromptIgnoresWorkspaceSkip(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", true, nil)
	requests := svc.Subscribe(t.Context())

	ctx, cancel := context.WithCancel(WithPolicy(t.Context(), SessionPolicyPrompt))
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		granted, _ := svc.Request(ctx, policyRequest("delegated", "tc"))
		done <- granted
	}()
	select {
	case ev := <-requests:
		require.Equal(t, "delegated", ev.Payload.SessionID, "a delegated run is asked even under workspace yolo")
	case <-done:
		t.Fatal("a delegated run must not be auto-approved by the workspace skip toggle")
	case <-time.After(time.Second):
		t.Fatal("request was never published")
	}
	cancel()
	require.False(t, <-done)

	granted, err := svc.Request(t.Context(), policyRequest("tui", "tc2"))
	require.NoError(t, err)
	require.True(t, granted, "turns without an explicit policy still follow the workspace toggle")
}

func TestContextPolicy_AllowAndDeny(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil)
	granted, err := svc.Request(WithPolicy(t.Context(), SessionPolicyAllow), policyRequest("s", "a"))
	require.NoError(t, err)
	require.True(t, granted)

	granted, err = svc.Request(WithPolicy(t.Context(), SessionPolicyDeny), policyRequest("s", "b"))
	require.NoError(t, err)
	require.False(t, granted)
	require.Equal(t, SessionPolicyPrompt, svc.SessionPolicy("s"), "run policies never change the session default")
}

func TestEffectivePolicy_AutoApprovedChildStaysApprovedUnderDelegate(t *testing.T) {
	t.Parallel()

	svc := NewPermissionService("/tmp", false, nil)
	svc.AutoApproveSession("fetch-child")
	p, _ := EffectivePolicy(WithPolicy(t.Context(), SessionPolicyPrompt), svc, "fetch-child")
	require.Equal(t, SessionPolicyAllow, p, "the caller already approved the fetch that started the sub-agent")

	granted, err := svc.Request(WithPolicy(t.Context(), SessionPolicyPrompt), policyRequest("fetch-child", "tc"))
	require.NoError(t, err)
	require.True(t, granted)
}
