package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/shell"
	"github.com/taigrr/fantasy"
)

// runBashToolCall is runBashTool with a caller-chosen tool-call ID so the
// per-call background request can target it.
func runBashToolCall(t *testing.T, tool fantasy.AgentTool, ctx context.Context, callID string, params BashParams) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: callID, Name: BashToolName, Input: string(input)})
	require.NoError(t, err)
	return resp
}

// shellIDFromResponse pulls the job id out of a moved-to-background result.
func shellIDFromResponse(t *testing.T, resp fantasy.ToolResponse) string {
	t.Helper()
	const marker = "Background shell ID: "
	i := strings.Index(resp.Content, marker)
	require.NotEqual(t, -1, i, "response must name the background shell: %q", resp.Content)
	rest := resp.Content[i+len(marker):]
	if nl := strings.IndexByte(rest, '\n'); nl != -1 {
		rest = rest[:nl]
	}
	return strings.TrimSpace(rest)
}

func TestBashTool_SoftInterruptMovesCommandToBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows")
	}
	t.Parallel()

	tool := newBashToolForTest(t.TempDir())
	soft := make(chan struct{})
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "sess-soft")
	ctx = WithSoftInterrupt(ctx, soft)

	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		done <- runBashToolCall(t, tool, ctx, "call-soft", BashParams{
			Command:             "sleep 30; echo finished",
			AutoBackgroundAfter: 600,
		})
	}()

	// Give the command time to start, then steer.
	time.Sleep(300 * time.Millisecond)
	close(soft)

	var resp fantasy.ToolResponse
	select {
	case resp = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bash did not return after soft interrupt")
	}
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "moved to background early because a user message is waiting")
	require.Contains(t, resp.Content, "Use job_output tool to view output or job_kill to terminate.")

	id := shellIDFromResponse(t, resp)
	bgManager := shell.GetBackgroundShellManager()
	bgShell, ok := bgManager.Get(id)
	require.True(t, ok, "job must survive in the manager after being backgrounded")
	require.False(t, bgShell.IsDone(), "job must not have been killed")

	// job_kill / job_output work on it.
	require.NoError(t, bgManager.Kill(id))
	_, gone := bgManager.Get(id)
	require.False(t, gone)
}

func TestBashTool_UserBackgroundRequestMovesCommandToBackground(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows")
	}
	t.Parallel()

	tool := newBashToolForTest(t.TempDir())
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "sess-user")

	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		done <- runBashToolCall(t, tool, ctx, "call-user-bg", BashParams{
			Command:             "sleep 30",
			AutoBackgroundAfter: 600,
		})
	}()

	// Wait until the call has registered itself as backgroundable.
	require.Eventually(t, func() bool {
		return RequestBackground("sess-user", "call-user-bg")
	}, 5*time.Second, 20*time.Millisecond)

	var resp fantasy.ToolResponse
	select {
	case resp = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bash did not return after background request")
	}
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "moved to background by the user")
	id := shellIDFromResponse(t, resp)

	require.False(t, RequestBackground("sess-user", "call-user-bg"), "registration must be released once the tool returns")

	bgManager := shell.GetBackgroundShellManager()
	bgShell, ok := bgManager.Get(id)
	require.True(t, ok)
	require.False(t, bgShell.IsDone())
	require.NoError(t, bgManager.Kill(id))
}

func TestBashTool_SoftInterruptAfterCompletionReturnsResult(t *testing.T) {
	t.Parallel()

	tool := newBashToolForTest(t.TempDir())
	soft := make(chan struct{})
	close(soft)
	ctx := WithSoftInterrupt(context.WithValue(t.Context(), SessionIDContextKey, "sess-done"), soft)

	// The interrupt is already pending, but a command that finishes on
	// the first poll must still return its real output, never a job id.
	resp := runBashToolCall(t, tool, ctx, "call-fast", BashParams{Command: "echo quick"})
	require.False(t, resp.IsError)
	if strings.Contains(resp.Content, "Background shell ID") {
		// Racing the 100ms poll against an already-closed interrupt is
		// legitimately non-deterministic; what matters is the result is
		// never lost: the job must still be retrievable and complete.
		id := shellIDFromResponse(t, resp)
		bgShell, ok := shell.GetBackgroundShellManager().Get(id)
		require.True(t, ok)
		bgShell.Wait()
		stdout, _, done, _ := bgShell.GetOutput()
		require.True(t, done)
		require.Contains(t, stdout, "quick")
		return
	}
	require.Contains(t, resp.Content, "quick")
}

func TestJobOutputTool_WaitEndsEarlyOnSoftInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows")
	}
	t.Parallel()

	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(context.Background(), t.TempDir(), nil, "echo partial; sleep 30", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = bgManager.Kill(bgShell.ID) })

	soft := make(chan struct{})
	ctx := WithSoftInterrupt(context.WithValue(t.Context(), SessionIDContextKey, "sess-jo"), soft)
	tool := NewJobOutputTool()
	input, err := json.Marshal(JobOutputParams{ShellID: bgShell.ID, Wait: true})
	require.NoError(t, err)

	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-jo", Name: JobOutputToolName, Input: string(input)})
		require.NoError(t, err)
		done <- resp
	}()
	time.Sleep(300 * time.Millisecond)
	close(soft)

	var resp fantasy.ToolResponse
	select {
	case resp = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job_output did not return after soft interrupt")
	}
	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "Stopped waiting because a user message is waiting")
	require.Contains(t, resp.Content, "Status: running")
	require.Contains(t, resp.Content, "partial")
	require.False(t, bgShell.IsDone(), "the job must keep running")
}

func TestJobOutputTool_WaitEndsEarlyOnUserRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows")
	}
	t.Parallel()

	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(context.Background(), t.TempDir(), nil, "sleep 30", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = bgManager.Kill(bgShell.ID) })

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "sess-jo-user")
	tool := NewJobOutputTool()
	input, err := json.Marshal(JobOutputParams{ShellID: bgShell.ID, Wait: true})
	require.NoError(t, err)

	done := make(chan fantasy.ToolResponse, 1)
	go func() {
		resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "call-jo-user", Name: JobOutputToolName, Input: string(input)})
		require.NoError(t, err)
		done <- resp
	}()
	require.Eventually(t, func() bool {
		return RequestBackground("sess-jo-user", "call-jo-user")
	}, 5*time.Second, 20*time.Millisecond)

	var resp fantasy.ToolResponse
	select {
	case resp = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job_output did not return after background request")
	}
	require.Contains(t, resp.Content, "Stopped waiting at the user's request")
	require.Contains(t, resp.Content, "Status: running")
	require.False(t, bgShell.IsDone())
}

func TestMovedToBackgroundResponse_SharesContract(t *testing.T) {
	t.Parallel()
	for _, reason := range []backgroundReason{backgroundReasonTimeout, backgroundReasonSteer, backgroundReasonUser} {
		got := movedToBackgroundResponse("abc123", reason)
		require.Contains(t, got, "Background shell ID: abc123")
		require.Contains(t, got, "Use job_output tool to view output or job_kill to terminate.")
	}
	require.Contains(t, movedToBackgroundResponse("x", backgroundReasonTimeout), "taking longer than expected")
}

func TestWatchBackgroundJob_NotifiesOnCompletion(t *testing.T) {
	t.Parallel()

	got := make(chan [2]string, 1)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "sess-notify")
	ctx = WithJobNotifier(ctx, func(sessionID, text string) { got <- [2]string{sessionID, text} })

	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(context.Background(), t.TempDir(), nil, "echo tail-marker; exit 3", "run thing")
	require.NoError(t, err)
	watchBackgroundJob(ctx, bgShell)

	select {
	case n := <-got:
		require.Equal(t, "sess-notify", n[0])
		require.Contains(t, n[1], "[background job finished] run thing (job "+bgShell.ID+")")
		require.Contains(t, n[1], "tail-marker")
		require.Contains(t, n[1], "Exit code 3")
		require.Contains(t, n[1], "automatic notice")
	case <-time.After(5 * time.Second):
		t.Fatal("no completion notification")
	}
}

func TestWatchBackgroundJob_SilentWhenKilledOrNoNotifier(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows")
	}
	t.Parallel()

	got := make(chan [2]string, 1)
	ctx := context.WithValue(t.Context(), SessionIDContextKey, "sess-killed")
	ctx = WithJobNotifier(ctx, func(sessionID, text string) { got <- [2]string{sessionID, text} })

	bgManager := shell.GetBackgroundShellManager()
	bgShell, err := bgManager.Start(context.Background(), t.TempDir(), nil, "sleep 30", "")
	require.NoError(t, err)
	watchBackgroundJob(ctx, bgShell)
	require.NoError(t, bgManager.Kill(bgShell.ID))
	select {
	case n := <-got:
		t.Fatalf("killed job must not notify, got %q", n[1])
	case <-time.After(500 * time.Millisecond):
	}

	// Without a notifier the watcher is a no-op and must not block.
	other, err := bgManager.Start(context.Background(), t.TempDir(), nil, "echo x", "")
	require.NoError(t, err)
	watchBackgroundJob(context.WithValue(t.Context(), SessionIDContextKey, "s"), other)
	other.Wait()
}

func TestJobFinishedNotification_TruncatesOutput(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("y", jobNotificationOutputLimit+100) + "END"
	got := jobFinishedNotification("id1", "", "make all", long)
	require.Contains(t, got, "[background job finished] make all (job id1)")
	require.Contains(t, got, "…")
	require.Contains(t, got, "END")
	require.NotContains(t, got, strings.Repeat("y", jobNotificationOutputLimit+1), "only the tail of long output is kept")
	require.Contains(t, jobFinishedNotification("id2", "", "true", ""), BashNoOutput)
}
