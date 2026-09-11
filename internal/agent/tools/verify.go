package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/shell"
)

// verifyTimeout bounds how long a post-edit verify command may run; it is
// synchronous by design so its output lands in the same tool result.
const verifyTimeout = 90 * time.Second

// runVerify runs an optional shell command after a successful file write
// and returns its output wrapped in a <verify> block for the model. It
// applies the same permission gate as the bash tool so verify cannot be
// used to sidestep command approval. An empty command yields "".
func runVerify(ctx context.Context, permissions permission.Service, workingDir, command, callID string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}

	wrap := func(body string) string {
		return fmt.Sprintf("\n<verify command=%q>\n%s\n</verify>\n", command, strings.TrimRight(body, "\n"))
	}

	sessionID := GetSessionFromContext(ctx)
	if sessionID == "" {
		return wrap("skipped: no session in context")
	}

	if !isSafeReadOnlyCommand(command) {
		ok, err := permissions.Request(ctx, permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        workingDir,
			ToolCallID:  callID,
			ToolName:    BashToolName,
			Action:      "execute",
			Description: fmt.Sprintf("Execute verify command: %s", command),
			Params:      BashPermissionsParams{Command: command, WorkingDir: workingDir},
		})
		if err != nil {
			return wrap("skipped: " + err.Error())
		}
		if !ok {
			return wrap("skipped: permission denied")
		}
	}

	bgManager := shell.GetBackgroundShellManager()
	bgManager.Cleanup()
	bgShell, err := bgManager.Start(context.Background(), workingDir, blockFuncs(permissions.SysadminMode()), command, "verify")
	if err != nil {
		return wrap("error starting shell: " + err.Error())
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.After(verifyTimeout)
	for {
		select {
		case <-ticker.C:
			stdout, stderr, done, execErr := bgShell.GetOutput()
			if !done {
				continue
			}
			bgManager.Remove(bgShell.ID)
			out := formatOutput(stdout, stderr, execErr)
			if out == "" {
				out = BashNoOutput
			}
			return wrap(out)
		case <-deadline:
			bgManager.Kill(bgShell.ID)
			return wrap(fmt.Sprintf("timed out after %s; command killed. Run it via the bash tool instead.", verifyTimeout))
		case <-ctx.Done():
			bgManager.Kill(bgShell.ID)
			return wrap("cancelled")
		}
	}
}
