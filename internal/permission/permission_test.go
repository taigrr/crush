package permission

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/pubsub"
)

func TestPermissionService_AllowedCommands(t *testing.T) {
	tests := []struct {
		name         string
		allowedTools []string
		toolName     string
		action       string
		expected     bool
	}{
		{
			name:         "tool in allowlist",
			allowedTools: []string{"bash", "view"},
			toolName:     "bash",
			action:       "execute",
			expected:     true,
		},
		{
			name:         "tool:action in allowlist",
			allowedTools: []string{"bash:execute", "edit:create"},
			toolName:     "bash",
			action:       "execute",
			expected:     true,
		},
		{
			name:         "tool not in allowlist",
			allowedTools: []string{"view", "ls"},
			toolName:     "bash",
			action:       "execute",
			expected:     false,
		},
		{
			name:         "tool:action not in allowlist",
			allowedTools: []string{"bash:read", "edit:create"},
			toolName:     "bash",
			action:       "execute",
			expected:     false,
		},
		{
			name:         "empty allowlist",
			allowedTools: []string{},
			toolName:     "bash",
			action:       "execute",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewPermissionService("/tmp", false, tt.allowedTools)

			// Create a channel to capture the permission request
			// Since we're testing the allowlist logic, we need to simulate the request
			ps := service.(*permissionService)

			// Test the allowlist logic directly
			commandKey := tt.toolName + ":" + tt.action
			allowed := false
			for _, cmd := range ps.allowedTools {
				if cmd == commandKey || cmd == tt.toolName {
					allowed = true
					break
				}
			}

			if allowed != tt.expected {
				t.Errorf("expected %v, got %v for tool %s action %s with allowlist %v",
					tt.expected, allowed, tt.toolName, tt.action, tt.allowedTools)
			}
		})
	}
}

func TestSkipRace(t *testing.T) {
	svc := NewPermissionService("/tmp", false, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		svc.SetSkipRequests(true)
	}()
	go func() {
		defer wg.Done()
		svc.SkipRequests()
	}()
	wg.Wait()
}

func TestPermissionService_SkipMode(t *testing.T) {
	service := NewPermissionService("/tmp", true, []string{})

	result, err := service.Request(t.Context(), CreatePermissionRequest{
		SessionID:   "test-session",
		ToolName:    "bash",
		Action:      "execute",
		Description: "test command",
		Path:        "/tmp",
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected permission to be granted in skip mode")
	}
}

func TestPermissionService_HookApproval(t *testing.T) {
	t.Parallel()

	t.Run("matching tool call ID short-circuits the prompt", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/tmp", false, nil)

		ctx := WithHookApproval(t.Context(), "call-42")
		granted, err := service.Request(ctx, CreatePermissionRequest{
			SessionID:   "s1",
			ToolCallID:  "call-42",
			ToolName:    "bash",
			Action:      "execute",
			Description: "hook-approved command",
			Path:        "/tmp",
		})
		require.NoError(t, err)
		assert.True(t, granted, "hook-approved call should bypass the prompt")
	})

	t.Run("approval is scoped to the stamped tool call ID", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/tmp", false, nil)

		// Stamp for call-42, ask for a different call ID — must not leak.
		ctx := WithHookApproval(t.Context(), "call-42")

		// Kick off a real request that will need a subscriber to resolve it.
		events := service.Subscribe(t.Context())
		var (
			wg      sync.WaitGroup
			granted bool
			err     error
		)
		wg.Go(func() {
			granted, err = service.Request(ctx, CreatePermissionRequest{
				SessionID:   "s1",
				ToolCallID:  "call-other",
				ToolName:    "bash",
				Action:      "execute",
				Description: "unrelated call",
				Path:        "/tmp",
			})
		})

		// Confirm the service published a real request (i.e. didn't bypass).
		event := <-events
		service.Deny(event.Payload)
		wg.Wait()
		require.NoError(t, err)
		assert.False(t, granted, "stamped approval must not apply to a different tool call")
	})

	t.Run("notifies subscribers that permission was granted", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/tmp", false, nil)

		notifications := service.SubscribeNotifications(t.Context())

		ctx := WithHookApproval(t.Context(), "call-99")
		granted, err := service.Request(ctx, CreatePermissionRequest{
			SessionID:  "s1",
			ToolCallID: "call-99",
			ToolName:   "view",
			Action:     "read",
			Path:       "/tmp",
		})
		require.NoError(t, err)
		assert.True(t, granted)

		event := <-notifications
		assert.Equal(t, "call-99", event.Payload.ToolCallID)
		assert.True(t, event.Payload.Granted, "subscribers should see a granted notification")
	})
}

func TestPermissionService_SequentialProperties(t *testing.T) {
	t.Run("Sequential permission requests with persistent grants", func(t *testing.T) {
		service := NewPermissionService("/tmp", false, []string{})

		req1 := CreatePermissionRequest{
			SessionID:   "session1",
			ToolName:    "file_tool",
			Description: "Read file",
			Action:      "read",
			Params:      map[string]string{"file": "test.txt"},
			Path:        "/tmp/test.txt",
		}

		var result1 bool
		var wg sync.WaitGroup
		wg.Add(1)

		events := service.Subscribe(t.Context())

		go func() {
			defer wg.Done()
			result1, _ = service.Request(t.Context(), req1)
		}()

		var permissionReq PermissionRequest
		event := <-events

		permissionReq = event.Payload
		service.GrantPersistent(permissionReq)

		wg.Wait()
		assert.True(t, result1, "First request should be granted")

		// Second identical request should be automatically approved due to persistent permission
		req2 := CreatePermissionRequest{
			SessionID:   "session1",
			ToolName:    "file_tool",
			Description: "Read file again",
			Action:      "read",
			Params:      map[string]string{"file": "test.txt"},
			Path:        "/tmp/test.txt",
		}
		result2, err := service.Request(t.Context(), req2)
		require.NoError(t, err)
		assert.True(t, result2, "Second request should be auto-approved")
	})
	t.Run("Sequential requests with temporary grants", func(t *testing.T) {
		service := NewPermissionService("/tmp", false, []string{})

		req := CreatePermissionRequest{
			SessionID:   "session2",
			ToolName:    "file_tool",
			Description: "Write file",
			Action:      "write",
			Params:      map[string]string{"file": "test.txt"},
			Path:        "/tmp/test.txt",
		}

		events := service.Subscribe(t.Context())
		var result1 bool
		var wg sync.WaitGroup

		wg.Go(func() {
			result1, _ = service.Request(t.Context(), req)
		})

		var permissionReq PermissionRequest
		event := <-events
		permissionReq = event.Payload

		service.Grant(permissionReq)
		wg.Wait()
		assert.True(t, result1, "First request should be granted")

		var result2 bool

		wg.Go(func() {
			result2, _ = service.Request(t.Context(), req)
		})

		event = <-events
		permissionReq = event.Payload
		service.Deny(permissionReq)
		wg.Wait()
		assert.False(t, result2, "Second request should be denied")
	})
	t.Run("Concurrent requests with different outcomes", func(t *testing.T) {
		service := NewPermissionService("/tmp", false, []string{})

		events := service.Subscribe(t.Context())

		var wg sync.WaitGroup
		results := make([]bool, 3)

		requests := []CreatePermissionRequest{
			{
				SessionID:   "concurrent1",
				ToolName:    "tool1",
				Action:      "action1",
				Path:        "/tmp/file1.txt",
				Description: "First concurrent request",
			},
			{
				SessionID:   "concurrent2",
				ToolName:    "tool2",
				Action:      "action2",
				Path:        "/tmp/file2.txt",
				Description: "Second concurrent request",
			},
			{
				SessionID:   "concurrent3",
				ToolName:    "tool3",
				Action:      "action3",
				Path:        "/tmp/file3.txt",
				Description: "Third concurrent request",
			},
		}

		for i, req := range requests {
			wg.Add(1)
			go func(index int, request CreatePermissionRequest) {
				defer wg.Done()
				result, _ := service.Request(t.Context(), request)
				results[index] = result
			}(i, req)
		}

		for range 3 {
			event := <-events
			switch event.Payload.ToolName {
			case "tool1":
				service.Grant(event.Payload)
			case "tool2":
				service.GrantPersistent(event.Payload)
			case "tool3":
				service.Deny(event.Payload)
			}
		}
		wg.Wait()
		grantedCount := 0
		for _, result := range results {
			if result {
				grantedCount++
			}
		}

		assert.Equal(t, 2, grantedCount, "Should have 2 granted and 1 denied")
		secondReq := requests[1]
		secondReq.Description = "Repeat of second request"
		result, err := service.Request(t.Context(), secondReq)
		require.NoError(t, err)
		assert.True(t, result, "Repeated request should be auto-approved due to persistent permission")
	})
}

// TestPermissionService_ResolveIdempotency covers the multi-subscriber
// resolve guarantees added for client/server mode: exactly one
// notification per resolution, racing callers see "already resolved",
// and stray Grant/Deny calls for unknown IDs are safe no-ops.
func TestPermissionService_ResolveIdempotency(t *testing.T) {
	t.Parallel()

	t.Run("concurrent grants resolve exactly once", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/tmp", false, nil)

		events := service.Subscribe(t.Context())
		notifications := service.SubscribeNotifications(t.Context())

		req := CreatePermissionRequest{
			SessionID:  "race-session",
			ToolCallID: "race-call",
			ToolName:   "tool",
			Action:     "act",
			Path:       "/tmp/race",
		}

		var (
			wg         sync.WaitGroup
			granted    bool
			requestErr error
		)
		wg.Go(func() {
			granted, requestErr = service.Request(t.Context(), req)
		})

		// Wait for the request to be published so we have a real
		// PermissionRequest (with its server-side ID) to race on.
		var pending PermissionRequest
		select {
		case ev := <-events:
			pending = ev.Payload
		case <-time.After(2 * time.Second):
			t.Fatal("permission request was never published")
		}

		// Drain the initial "request opened" notification (Granted ==
		// false && Denied == false) so the next read is the resolution
		// itself.
		select {
		case ev := <-notifications:
			require.False(t, ev.Payload.Granted, "initial notification must not be granted")
			require.False(t, ev.Payload.Denied, "initial notification must not be denied")
		case <-time.After(2 * time.Second):
			t.Fatal("initial notification was never published")
		}

		// Race two grants from two goroutines.
		var (
			resolvedCount atomic.Int32
			start         = make(chan struct{})
			racers        sync.WaitGroup
		)
		for range 2 {
			racers.Go(func() {
				<-start
				if service.Grant(pending) {
					resolvedCount.Add(1)
				}
			})
		}
		close(start)
		racers.Wait()

		// Original Request must return granted exactly once.
		wg.Wait()
		require.NoError(t, requestErr)
		assert.True(t, granted, "request should observe its grant")

		// Exactly one of the two grants resolved the request.
		assert.Equal(t, int32(1), resolvedCount.Load(),
			"exactly one Grant should report it resolved the request")

		// Exactly one resolution notification, and no further ones.
		select {
		case ev := <-notifications:
			assert.True(t, ev.Payload.Granted, "resolution notification should be granted")
			assert.Equal(t, "race-call", ev.Payload.ToolCallID)
		case <-time.After(2 * time.Second):
			t.Fatal("resolution notification was never published")
		}
		select {
		case ev := <-notifications:
			t.Fatalf("unexpected duplicate notification: %+v", ev.Payload)
		case <-time.After(50 * time.Millisecond):
			// good: no duplicate.
		}

		// pendingRequests must be empty: no goroutine is left blocked
		// on a send, and a future Grant for the same ID is a no-op.
		ps := service.(*permissionService)
		assert.Equal(t, 0, ps.pendingRequests.Len(),
			"pendingRequests must be empty after resolution")

		assert.False(t, service.Grant(pending),
			"a third Grant should report already-resolved")
	})

	t.Run("grant after deny is a no-op", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/tmp", false, nil)

		events := service.Subscribe(t.Context())
		notifications := service.SubscribeNotifications(t.Context())

		req := CreatePermissionRequest{
			SessionID:  "deny-first",
			ToolCallID: "df-call",
			ToolName:   "tool",
			Action:     "act",
			Path:       "/tmp/df",
		}

		var (
			wg         sync.WaitGroup
			granted    bool
			requestErr error
		)
		wg.Go(func() {
			granted, requestErr = service.Request(t.Context(), req)
		})

		var pending PermissionRequest
		select {
		case ev := <-events:
			pending = ev.Payload
		case <-time.After(2 * time.Second):
			t.Fatal("permission request was never published")
		}

		// Drain the initial neither-granted-nor-denied notification.
		<-notifications

		assert.True(t, service.Deny(pending), "Deny should resolve the request")
		wg.Wait()
		require.NoError(t, requestErr)
		assert.False(t, granted, "request should observe denial")

		// A follow-up Grant must be a no-op and must not flip the
		// outcome or publish anything new.
		assert.False(t, service.Grant(pending),
			"Grant after Deny should report already-resolved")

		select {
		case ev := <-notifications:
			// The first resolution notification (denial) is expected;
			// anything after that is a bug.
			require.True(t, ev.Payload.Denied,
				"the only post-initial notification must be the denial")
		case <-time.After(2 * time.Second):
			t.Fatal("denial notification was never published")
		}
		select {
		case ev := <-notifications:
			t.Fatalf("Grant after Deny must not publish: %+v", ev.Payload)
		case <-time.After(50 * time.Millisecond):
			// good.
		}
	})

	t.Run("losing GrantPersistent does not record session permission", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/tmp", false, nil)

		events := service.Subscribe(t.Context())
		notifications := service.SubscribeNotifications(t.Context())

		req := CreatePermissionRequest{
			SessionID:  "race-persist",
			ToolCallID: "rp-call",
			ToolName:   "tool",
			Action:     "act",
			Path:       "/tmp/rp",
		}

		var (
			wg         sync.WaitGroup
			granted    bool
			requestErr error
		)
		wg.Go(func() {
			granted, requestErr = service.Request(t.Context(), req)
		})

		// Wait for the request to be published so we have the real
		// pending PermissionRequest to race on.
		var pending PermissionRequest
		select {
		case ev := <-events:
			pending = ev.Payload
		case <-time.After(2 * time.Second):
			t.Fatal("permission request was never published")
		}

		// Drain the initial neither-granted-nor-denied notification.
		<-notifications

		// Deny wins, then a competing GrantPersistent loses.
		assert.True(t, service.Deny(pending), "Deny should resolve the request")
		assert.False(t, service.GrantPersistent(pending),
			"GrantPersistent after Deny should report already-resolved")

		wg.Wait()
		require.NoError(t, requestErr)
		assert.False(t, granted, "request should observe denial")

		// The losing GrantPersistent must not have inserted an
		// auto-approve entry. Issue a matching follow-up request and
		// confirm the service still publishes a pending request (i.e.
		// not auto-approved). We then Deny it to drain the goroutine.
		var (
			wg2         sync.WaitGroup
			granted2    bool
			requestErr2 error
		)
		wg2.Go(func() {
			granted2, requestErr2 = service.Request(t.Context(), req)
		})

		select {
		case ev := <-events:
			assert.Equal(t, pending.SessionID, ev.Payload.SessionID)
			service.Deny(ev.Payload)
		case <-time.After(2 * time.Second):
			t.Fatal("follow-up request was auto-approved; persistent grant leaked")
		}

		wg2.Wait()
		require.NoError(t, requestErr2)
		assert.False(t, granted2, "follow-up request should be denied, not auto-approved")
	})

	t.Run("grant for unknown id is a safe no-op", func(t *testing.T) {
		t.Parallel()
		service := NewPermissionService("/tmp", false, nil)

		notifications := service.SubscribeNotifications(t.Context())

		bogus := PermissionRequest{
			ID:         "does-not-exist",
			ToolCallID: "ghost",
			ToolName:   "tool",
			Action:     "act",
			Path:       "/tmp/ghost",
		}

		assert.NotPanics(t, func() {
			assert.False(t, service.Grant(bogus),
				"Grant for unknown ID should report already-resolved")
			assert.False(t, service.GrantPersistent(bogus),
				"GrantPersistent for unknown ID should report already-resolved")
			assert.False(t, service.Deny(bogus),
				"Deny for unknown ID should report already-resolved")
		})

		select {
		case ev := <-notifications:
			t.Fatalf("unknown-ID resolution must not publish: %+v", ev.Payload)
		case <-time.After(50 * time.Millisecond):
			// good: no notification.
		}
	})
}

// TestPermissionService_CancelPublishesDenial verifies that a pending
// permission request whose context is cancelled (e.g. the agent run was
// cancelled) resolves as denied and publishes a notification, so any
// open permission dialog on subscribed clients is dismissed instead of
// hanging open.
func TestPermissionService_CancelPublishesDenial(t *testing.T) {
	t.Parallel()
	service := NewPermissionService("/tmp", false, nil)

	requests := service.Subscribe(t.Context())
	notifications := service.SubscribeNotifications(t.Context())

	ctx, cancel := context.WithCancel(t.Context())

	var granted bool
	var err error
	var wg sync.WaitGroup
	wg.Go(func() {
		granted, err = service.Request(ctx, CreatePermissionRequest{
			SessionID:  "s-cancel",
			ToolCallID: "call-cancel",
			ToolName:   "view",
			Action:     "read",
			Path:       "/tmp",
		})
	})

	// Wait for the request to be published (the initial pending
	// notification is published first, then the request itself).
	<-requests

	// Cancel the run while the prompt is still pending.
	cancel()
	wg.Wait()

	require.Error(t, err)
	assert.False(t, granted)

	// Drain notifications until the terminal denial arrives.
	deadline := time.After(time.Second)
	var sawDenial bool
	for !sawDenial {
		select {
		case ev := <-notifications:
			if ev.Payload.ToolCallID == "call-cancel" && ev.Payload.Denied {
				assert.Equal(t, "s-cancel", ev.Payload.SessionID)
				sawDenial = true
			}
		case <-deadline:
			t.Fatal("cancellation did not publish a denial notification")
		}
	}
}

// TestPermissionService_NoCrossSessionHeadOfLineBlocking verifies that
// an unresolved permission request in one session does NOT block a
// request from a different session. A workspace runs many sessions
// concurrently (e.g. swarm-dispatched background turns); a single
// workspace-wide mutex held across the blocking wait would wedge every
// other session's prompt behind one in-flight request. Locking is now
// per session, so session-b's request must publish and resolve while
// session-a's request is still pending.
func TestPermissionService_NoCrossSessionHeadOfLineBlocking(t *testing.T) {
	t.Parallel()
	service := NewPermissionService("/tmp", false, nil)

	requests := service.Subscribe(t.Context())

	// Request A blocks in session-a; we never resolve it until the end.
	ctxA, cancelA := context.WithCancel(t.Context())
	defer cancelA()
	go func() {
		_, _ = service.Request(ctxA, CreatePermissionRequest{
			SessionID:  "session-a",
			ToolCallID: "call-a",
			ToolName:   "view",
			Action:     "read",
			Path:       "/tmp",
		})
	}()

	// Wait for A's request to be published so it is genuinely in-flight
	// and holding session-a's lock.
	reqA := waitForRequest(t, requests, "call-a")
	require.Equal(t, "session-a", reqA.SessionID)

	// Request B in a DIFFERENT session must publish even though A is
	// still pending. If a workspace-wide lock were held across A's wait,
	// this Request would never publish and the drain below would time
	// out.
	var bDone sync.WaitGroup
	bDone.Add(1)
	var grantedB bool
	go func() {
		defer bDone.Done()
		grantedB, _ = service.Request(t.Context(), CreatePermissionRequest{
			SessionID:  "session-b",
			ToolCallID: "call-b",
			ToolName:   "view",
			Action:     "read",
			Path:       "/tmp",
		})
	}()

	reqB := waitForRequest(t, requests, "call-b")
	require.Equal(t, "session-b", reqB.SessionID,
		"session-b's request must publish while session-a is still pending")

	// Resolve B; it must complete independently of A.
	require.True(t, service.Grant(reqB))
	bDone.Wait()
	assert.True(t, grantedB, "session-b's request resolves independently")

	// A is still pending; cancel it to unblock the goroutine.
	cancelA()
}

// waitForRequest drains the request channel until a request with the
// given tool call ID arrives (or the test times out).
func waitForRequest(t *testing.T, ch <-chan pubsub.Event[PermissionRequest], toolCallID string) PermissionRequest {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Payload.ToolCallID == toolCallID {
				return ev.Payload
			}
		case <-deadline:
			t.Fatalf("timed out waiting for request %q", toolCallID)
		}
	}
}

// TestPermissionService_CancelAll unblocks every pending request as
// denied and publishes a resolution notification for each, so shutdown
// leaves no blocked agent goroutine and no zombie prompt.
func TestPermissionService_CancelAll(t *testing.T) {
	t.Parallel()
	svc := NewPermissionService("/tmp", false, nil)

	notifs := svc.SubscribeNotifications(t.Context())
	requests := svc.Subscribe(t.Context())

	type res struct {
		granted bool
		err     error
	}
	done := make(chan res, 2)
	for _, sid := range []string{"s-a", "s-b"} {
		go func(sid string) {
			g, err := svc.Request(t.Context(), CreatePermissionRequest{
				SessionID:  sid,
				ToolCallID: "tc-" + sid,
				ToolName:   "view",
				Action:     "read",
				Path:       "/tmp",
			})
			done <- res{granted: g, err: err}
		}(sid)
	}

	// Wait until both requests have been published (order-independent:
	// collect from the shared channel without discarding either).
	seen := map[string]bool{}
	reqDeadline := time.After(2 * time.Second)
	for len(seen) < 2 {
		select {
		case ev := <-requests:
			seen[ev.Payload.ToolCallID] = true
		case <-reqDeadline:
			t.Fatalf("timed out waiting for both requests, saw %v", seen)
		}
	}

	svc.CancelAll()

	for range 2 {
		select {
		case r := <-done:
			require.False(t, r.granted, "cancelled request must not be granted")
		case <-time.After(2 * time.Second):
			t.Fatal("CancelAll did not unblock a pending Request")
		}
	}

	// Two terminal (denied) notifications must have been published.
	denied := 0
	deadline := time.After(time.Second)
	for denied < 2 {
		select {
		case ev := <-notifs:
			if ev.Payload.Denied {
				denied++
			}
		case <-deadline:
			t.Fatalf("expected 2 denied notifications, saw %d", denied)
		}
	}
}

// TestPermissionService_RepublishPending re-emits the request event for
// the given session only, so a client that just switched to it surfaces
// the prompt (switch-to-grant).
func TestPermissionService_RepublishPending(t *testing.T) {
	t.Parallel()
	svc := NewPermissionService("/tmp", false, nil)

	requests := svc.Subscribe(t.Context())

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		_, _ = svc.Request(ctx, CreatePermissionRequest{
			SessionID:  "s-target",
			ToolCallID: "tc-target",
			ToolName:   "view",
			Action:     "read",
			Path:       "/tmp",
		})
	}()
	first := waitForRequest(t, requests, "tc-target")
	require.Equal(t, "s-target", first.SessionID)

	// Republishing a different session must emit nothing for our target.
	svc.RepublishPending("s-other")
	// Republishing the target session re-emits its request.
	svc.RepublishPending("s-target")
	again := waitForRequest(t, requests, "tc-target")
	require.Equal(t, first.ID, again.ID, "republished request must be the same pending request")
}
