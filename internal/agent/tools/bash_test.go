package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/shell"
	"github.com/taigrr/fantasy"
)

type mockBashPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
}

func (m *mockBashPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	return true, nil
}

func (m *mockBashPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *mockBashPermissionService) CancelAll() {}

func (m *mockBashPermissionService) RepublishPending(sessionID string) {}

func (m *mockBashPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *mockBashPermissionService) AutoApproveSession(sessionID string) {}

func (m *mockBashPermissionService) SetSkipRequests(skip bool) {}

func (m *mockBashPermissionService) SkipRequests() bool {
	return false
}

func (m *mockBashPermissionService) SetSysadminMode(enabled bool) {}

func (m *mockBashPermissionService) SysadminMode() bool {
	return false
}

func (m *mockBashPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func TestBashTool_DefaultAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "default threshold",
		Command:     "echo done",
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.False(t, meta.Background)
	require.Empty(t, meta.ShellID)
	require.Contains(t, meta.Output, "done")
}

func TestBashTool_CustomAutoBackgroundThreshold(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:         "custom threshold",
		Command:             "sleep 1.5 && echo done",
		AutoBackgroundAfter: 1,
	})

	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Background)
	require.NotEmpty(t, meta.ShellID)
	require.Contains(t, resp.Content, "moved to background")

	bgManager := shell.GetBackgroundShellManager()
	require.NoError(t, bgManager.Kill(meta.ShellID))
}

type recordingPermissionService struct {
	*pubsub.Broker[permission.PermissionRequest]
	requestCount int
	allow        bool
}

func (m *recordingPermissionService) Request(ctx context.Context, req permission.CreatePermissionRequest) (bool, error) {
	m.requestCount++
	return m.allow, nil
}

func (m *recordingPermissionService) Grant(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) Deny(req permission.PermissionRequest) bool { return true }

func (m *recordingPermissionService) CancelAll() {}

func (m *recordingPermissionService) RepublishPending(sessionID string) {}

func (m *recordingPermissionService) GrantPersistent(req permission.PermissionRequest) bool {
	return true
}

func (m *recordingPermissionService) AutoApproveSession(sessionID string) {}

func (m *recordingPermissionService) SetSkipRequests(skip bool) {}

func (m *recordingPermissionService) SkipRequests() bool {
	return false
}

func (m *recordingPermissionService) SetSysadminMode(enabled bool) {}

func (m *recordingPermissionService) SysadminMode() bool {
	return false
}

func (m *recordingPermissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[permission.PermissionNotification] {
	return make(<-chan pubsub.Event[permission.PermissionNotification])
}

func newBashToolForTest(workingDir string) fantasy.AgentTool {
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(permissions, func(context.Context) string { return workingDir }, attribution, "test-model", nil)
}

func newBashToolWithRecordingPerms(workingDir string, allow bool) (fantasy.AgentTool, *recordingPermissionService) {
	perms := &recordingPermissionService{
		Broker: pubsub.NewBroker[permission.PermissionRequest](),
		allow:  allow,
	}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	return NewBashTool(perms, func(context.Context) string { return workingDir }, attribution, "test-model", nil), perms
}

func TestBashTool_ChainedCommandsRequirePermission(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, true)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	// ls && echo should trigger permission check.
	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls",
		Command:     "ls && echo done",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 1, perms.requestCount, "chained command should trigger permission request")

	// Plain ls should NOT trigger permission check.
	perms.requestCount = 0
	resp = runBashTool(t, tool, ctx, BashParams{
		Description: "plain ls",
		Command:     "ls -la",
	})

	require.False(t, resp.IsError)
	require.Equal(t, 0, perms.requestCount, "plain ls should not trigger permission request")
}

func TestBashTool_ChainedCommandsDenied(t *testing.T) {
	workingDir := t.TempDir()
	tool, perms := newBashToolWithRecordingPerms(workingDir, false)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "chained ls denied",
		Command:     "ls && rm -rf /",
	})

	require.Equal(t, 1, perms.requestCount)
	require.Contains(t, resp.Content, "User denied permission")
}

func runBashTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params BashParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  BashToolName,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

func TestBashTool_BackgroundJobCompletionNotifies(t *testing.T) {
	workingDir := t.TempDir()
	type notice struct {
		sessionID string
		text      string
	}
	got := make(chan notice, 1)
	notify := func(ctx context.Context, sessionID, text string) error {
		got <- notice{sessionID: sessionID, text: text}
		return nil
	}
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(permissions, func(context.Context) string { return workingDir }, attribution, "test-model", notify)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:     "slow job",
		Command:         "sleep 2; echo job-output; exit 3",
		RunInBackground: true,
	})
	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.True(t, meta.Background)
	require.NotEmpty(t, meta.ShellID)

	select {
	case n := <-got:
		require.Equal(t, "test-session", n.sessionID)
		require.Contains(t, n.text, "[background job "+meta.ShellID+" finished]")
		require.Contains(t, n.text, "slow job")
		require.Contains(t, n.text, "job-output")
		require.Contains(t, n.text, "Exit code: 3")
	case <-time.After(10 * time.Second):
		t.Fatal("expected background job completion notification")
	}
}

func TestBashTool_KilledBackgroundJobDoesNotNotify(t *testing.T) {
	workingDir := t.TempDir()
	got := make(chan string, 1)
	notify := func(ctx context.Context, sessionID, text string) error {
		got <- text
		return nil
	}
	permissions := &mockBashPermissionService{Broker: pubsub.NewBroker[permission.PermissionRequest]()}
	attribution := &config.Attribution{TrailerStyle: config.TrailerStyleNone}
	tool := NewBashTool(permissions, func(context.Context) string { return workingDir }, attribution, "test-model", notify)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description:     "killed job",
		Command:         "sleep 30",
		RunInBackground: true,
	})
	require.False(t, resp.IsError)
	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.NotEmpty(t, meta.ShellID)
	require.NoError(t, shell.GetBackgroundShellManager().Kill(meta.ShellID))

	select {
	case text := <-got:
		t.Fatalf("unexpected notification for killed job: %s", text)
	case <-time.After(2 * time.Second):
	}
}

func TestDeriveBashDescription(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", MaxDerivedBashDescriptionLen+10)
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "empty", command: "", want: ""},
		{name: "whitespace only", command: " \n\t\n", want: ""},
		{name: "single line", command: "ls -la", want: "ls -la"},
		{name: "trims surrounding whitespace", command: "  go build ./...  ", want: "go build ./..."},
		{name: "first line only", command: "git status\ngit diff", want: "git status"},
		{name: "skips leading blank lines", command: "\n\n  echo hi\necho bye", want: "echo hi"},
		{name: "exactly at cap unchanged", command: strings.Repeat("a", MaxDerivedBashDescriptionLen), want: strings.Repeat("a", MaxDerivedBashDescriptionLen)},
		{name: "long line capped", command: long, want: strings.Repeat("a", MaxDerivedBashDescriptionLen-1) + "…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, DeriveBashDescription(tc.command))
		})
	}
}

func TestBashTool_DescriptionIsOptional(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	require.NotContains(t, tool.Info().Required, "description")
	require.Contains(t, tool.Info().Required, "command")
}

func TestBashTool_MissingDescriptionFallsBackToCommand(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Command: "echo fallback-output\necho second",
	})
	require.False(t, resp.IsError)

	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "echo fallback-output", meta.Description)
	require.Contains(t, meta.Output, "fallback-output")
}

func TestBashTool_ExplicitDescriptionPreserved(t *testing.T) {
	workingDir := t.TempDir()
	tool := newBashToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")

	resp := runBashTool(t, tool, ctx, BashParams{
		Description: "explicit",
		Command:     "echo hi",
	})
	require.False(t, resp.IsError)

	var meta BashResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "explicit", meta.Description)
}
