package tools

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"html/template"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/fsext"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/crush/internal/shell"
	"github.com/taigrr/fantasy"
)

type BashParams struct {
	Description         string `json:"description,omitempty" description:"A brief description of what the command does, try to keep it under 30 characters or so"`
	Command             string `json:"command" description:"The command to execute"`
	WorkingDir          string `json:"working_dir,omitempty" description:"The working directory to execute the command in (defaults to current directory)"`
	RunInBackground     bool   `json:"run_in_background,omitempty" description:"Set to true (boolean) to run this command in the background. Use job_output to read the output later."`
	AutoBackgroundAfter int    `json:"auto_background_after,omitempty" description:"Seconds to wait before automatically moving the command to a background job (default: 60)"`
}

type BashPermissionsParams struct {
	Description         string `json:"description"`
	Command             string `json:"command"`
	WorkingDir          string `json:"working_dir"`
	RunInBackground     bool   `json:"run_in_background"`
	AutoBackgroundAfter int    `json:"auto_background_after"`
}

type BashResponseMetadata struct {
	StartTime        int64  `json:"start_time"`
	EndTime          int64  `json:"end_time"`
	Output           string `json:"output"`
	Description      string `json:"description"`
	WorkingDirectory string `json:"working_directory"`
	Background       bool   `json:"background,omitempty"`
	ShellID          string `json:"shell_id,omitempty"`
	// BackgroundReason says why a foreground command ended up as a
	// background job: "timeout" (auto_background_after elapsed), "steer"
	// (a user message was waiting) or "user" (backgrounded from the UI).
	// Empty for commands that ran to completion or were started with
	// run_in_background.
	BackgroundReason string `json:"background_reason,omitempty"`
}

const (
	BashToolName = "bash"

	DefaultAutoBackgroundAfter = 60 // Commands taking longer automatically become background jobs
	MaxOutputLength            = 30000
	BashNoOutput               = "no output"
)

//go:embed bash.md.tpl
var bashDescriptionTmpl []byte

var bashDescriptionTpl = template.Must(
	template.New("bashDescription").
		Parse(string(bashDescriptionTmpl)),
)

type bashDescriptionData struct {
	SysadminCommands string
	MaxOutputLength  int
	Attribution      config.Attribution
	ModelName        string
	RgAvailable      bool
}

var sysadminCommands = []string{
	// Network/Download tools
	"alias",
	"aria2c",
	"axel",
	"chrome",
	"curl",
	"curlie",
	"firefox",
	"http-prompt",
	"httpie",
	"links",
	"lynx",
	"nc",
	"safari",
	"scp",
	"ssh",
	"telnet",
	"w3m",
	"wget",
	"xh",

	// System administration
	"doas",
	"su",
	"sudo",

	// Package managers
	"apk",
	"apt",
	"apt-cache",
	"apt-get",
	"dnf",
	"dpkg",
	"emerge",
	"home-manager",
	"makepkg",
	"opkg",
	"pacman",
	"paru",
	"pkg",
	"pkg_add",
	"pkg_delete",
	"portage",
	"rpm",
	"yay",
	"yum",
	"zypper",

	// System modification
	"at",
	"batch",
	"chkconfig",
	"crontab",
	"fdisk",
	"mkfs",
	"mount",
	"parted",
	"service",
	"systemctl",
	"umount",

	// Network configuration
	"firewall-cmd",
	"ifconfig",
	"ip",
	"iptables",
	"netstat",
	"pfctl",
	"route",
	"ufw",
}

func bashDescription(attribution *config.Attribution, modelName string) string {
	sysadminCommandsStr := strings.Join(sysadminCommands, ", ")
	var out bytes.Buffer
	if err := bashDescriptionTpl.Execute(&out, bashDescriptionData{
		SysadminCommands: sysadminCommandsStr,
		MaxOutputLength:  MaxOutputLength,
		Attribution:      *attribution,
		ModelName:        modelName,
		RgAvailable:      getRg() != "",
	}); err != nil {
		// this should never happen.
		panic("failed to execute bash description template: " + err.Error())
	}
	return out.String()
}

func blockFuncs(allowSysadmin bool) []shell.BlockFunc {
	funcs := []shell.BlockFunc{}
	if !allowSysadmin {
		funcs = append(funcs, shell.CommandsBlocker(sysadminCommands))
	}
	return append(
		funcs,
		// System package managers
		shell.ArgumentsBlocker("apk", []string{"add"}, nil),
		shell.ArgumentsBlocker("apt", []string{"install"}, nil),
		shell.ArgumentsBlocker("apt-get", []string{"install"}, nil),
		shell.ArgumentsBlocker("dnf", []string{"install"}, nil),
		shell.ArgumentsBlocker("pacman", nil, []string{"-S"}),
		shell.ArgumentsBlocker("pkg", []string{"install"}, nil),
		shell.ArgumentsBlocker("yum", []string{"install"}, nil),
		shell.ArgumentsBlocker("zypper", []string{"install"}, nil),

		// Language-specific package managers
		shell.ArgumentsBlocker("brew", []string{"install"}, nil),
		shell.ArgumentsBlocker("cargo", []string{"install"}, nil),
		shell.ArgumentsBlocker("gem", []string{"install"}, nil),
		shell.ArgumentsBlocker("go", []string{"install"}, nil),
		shell.ArgumentsBlocker("npm", []string{"install"}, []string{"--global"}),
		shell.ArgumentsBlocker("npm", []string{"install"}, []string{"-g"}),
		shell.ArgumentsBlocker("pip", []string{"install"}, []string{"--user"}),
		shell.ArgumentsBlocker("pip3", []string{"install"}, []string{"--user"}),
		shell.ArgumentsBlocker("pnpm", []string{"add"}, []string{"--global"}),
		shell.ArgumentsBlocker("pnpm", []string{"add"}, []string{"-g"}),
		shell.ArgumentsBlocker("yarn", []string{"global", "add"}, nil),

		// `go test -exec` can run arbitrary commands
		shell.ArgumentsBlocker("go", []string{"test"}, []string{"-exec"}),
	)
}

func NewBashTool(permissions permission.Service, workingDir WorkingDirFunc, attribution *config.Attribution, modelName string) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		BashToolName,
		string(bashDescription(attribution, modelName)),
		func(ctx context.Context, params BashParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Command == "" {
				return fantasy.NewTextErrorResponse("missing command"), nil
			}
			params.Description = cmp.Or(params.Description, DefaultBashDescription(params.Command))

			// Determine working directory
			execWorkingDir := cmp.Or(params.WorkingDir, workingDir(ctx))

			isSafeReadOnly := isSafeReadOnlyCommand(params.Command)

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.NewTextErrorResponse("session ID is required for executing shell command"), nil
			}
			if !isSafeReadOnly {
				p, err := permissions.Request(
					ctx,
					permission.CreatePermissionRequest{
						SessionID:   sessionID,
						Path:        execWorkingDir,
						ToolCallID:  call.ID,
						ToolName:    BashToolName,
						Action:      "execute",
						Description: fmt.Sprintf("Execute command: %s", params.Command),
						Params:      BashPermissionsParams(params),
					},
				)
				if err != nil {
					return fantasy.ToolResponse{}, err
				}
				if !p {
					return NewPermissionDeniedResponse(), nil
				}
			}

			// If explicitly requested as background, start immediately with detached context
			if params.RunInBackground {
				startTime := time.Now()
				bgManager := shell.GetBackgroundShellManager()
				bgManager.Cleanup()
				// Use background context so it continues after tool returns
				bgShell, err := bgManager.Start(context.Background(), execWorkingDir, blockFuncs(permissions.SysadminMode()), params.Command, params.Description)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("error starting background shell: %s", err)), nil
				}

				// Wait a short time to detect fast failures (blocked commands, syntax errors, etc.)
				time.Sleep(1 * time.Second)
				stdout, stderr, done, execErr := bgShell.GetOutput()

				if done {
					// Command failed or completed very quickly
					bgManager.Remove(bgShell.ID)

					interrupted := shell.IsInterrupt(execErr)
					exitCode := shell.ExitCode(execErr)
					if exitCode == 0 && !interrupted && execErr != nil {
						return fantasy.NewTextErrorResponse(fmt.Sprintf("[Job %s] error executing command: %s", bgShell.ID, execErr)), nil
					}

					stdout = formatOutput(stdout, stderr, execErr)

					metadata := BashResponseMetadata{
						StartTime:        startTime.UnixMilli(),
						EndTime:          time.Now().UnixMilli(),
						Output:           stdout,
						Description:      params.Description,
						Background:       params.RunInBackground,
						WorkingDirectory: bgShell.WorkingDir,
					}
					if stdout == "" {
						return fantasy.WithResponseMetadata(fantasy.NewTextResponse(BashNoOutput), metadata), nil
					}
					stdout += fmt.Sprintf("\n\n<cwd>%s</cwd>", normalizeWorkingDir(bgShell.WorkingDir))
					return fantasy.WithResponseMetadata(fantasy.NewTextResponse(stdout), metadata), nil
				}

				// Still running after fast-failure check - return as background job
				metadata := BashResponseMetadata{
					StartTime:        startTime.UnixMilli(),
					EndTime:          time.Now().UnixMilli(),
					Description:      params.Description,
					WorkingDirectory: bgShell.WorkingDir,
					Background:       true,
					ShellID:          bgShell.ID,
				}
				watchBackgroundJob(ctx, bgShell)
				response := fmt.Sprintf("Background shell started with ID: %s\n\nUse job_output tool to view output or job_kill to terminate.", bgShell.ID)
				return fantasy.WithResponseMetadata(fantasy.NewTextResponse(response), metadata), nil
			}

			// Start synchronous execution with auto-background support
			startTime := time.Now()

			// Start with detached context so it can survive if moved to background
			bgManager := shell.GetBackgroundShellManager()
			bgManager.Cleanup()
			bgShell, err := bgManager.Start(context.Background(), execWorkingDir, blockFuncs(permissions.SysadminMode()), params.Command, params.Description)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("error starting shell: %s", err)), nil
			}

			// Wait for either completion, auto-background threshold, a
			// background request, or context cancellation. Two things can
			// move the command to the background early without killing
			// it: a step-wide soft interrupt (a steer is waiting and the
			// model should see it now) and a per-call background request
			// (the user backgrounded this command from the UI). Both
			// return the same job-id response the auto-background path
			// does, with a note saying why it happened early.
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			autoBackgroundAfter := cmp.Or(params.AutoBackgroundAfter, DefaultAutoBackgroundAfter)
			autoBackgroundThreshold := time.Duration(autoBackgroundAfter) * time.Second
			timeout := time.After(autoBackgroundThreshold)
			bgRequested, releaseBg := RegisterBackgroundable(ctx, call.ID)
			defer releaseBg()

			var stdout, stderr string
			var done bool
			var execErr error
			var reason backgroundReason

		waitLoop:
			for {
				select {
				case <-ticker.C:
					stdout, stderr, done, execErr = bgShell.GetOutput()
					if done {
						break waitLoop
					}
				case <-timeout:
					reason = backgroundReasonTimeout
					stdout, stderr, done, execErr = bgShell.GetOutput()
					break waitLoop
				case <-SoftInterrupt(ctx):
					reason = backgroundReasonSteer
					stdout, stderr, done, execErr = bgShell.GetOutput()
					break waitLoop
				case <-bgRequested:
					reason = backgroundReasonUser
					stdout, stderr, done, execErr = bgShell.GetOutput()
					break waitLoop
				case <-ctx.Done():
					// Incoming context was cancelled before we moved to background
					// Kill the shell and return error
					bgManager.Kill(bgShell.ID)
					return fantasy.ToolResponse{}, ctx.Err()
				}
			}

			if done {
				// Command completed within threshold - return synchronously
				// Remove from background manager since we're returning directly
				// Don't call Kill() as it cancels the context and corrupts the exit code
				bgManager.Remove(bgShell.ID)

				interrupted := shell.IsInterrupt(execErr)
				exitCode := shell.ExitCode(execErr)
				if exitCode == 0 && !interrupted && execErr != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("[Job %s] error executing command: %s", bgShell.ID, execErr)), nil
				}

				stdout = formatOutput(stdout, stderr, execErr)

				metadata := BashResponseMetadata{
					StartTime:        startTime.UnixMilli(),
					EndTime:          time.Now().UnixMilli(),
					Output:           stdout,
					Description:      params.Description,
					Background:       params.RunInBackground,
					WorkingDirectory: bgShell.WorkingDir,
				}
				if stdout == "" {
					return fantasy.WithResponseMetadata(fantasy.NewTextResponse(BashNoOutput), metadata), nil
				}
				stdout += fmt.Sprintf("\n\n<cwd>%s</cwd>", normalizeWorkingDir(bgShell.WorkingDir))
				return fantasy.WithResponseMetadata(fantasy.NewTextResponse(stdout), metadata), nil
			}

			// Still running - keep as background job
			metadata := BashResponseMetadata{
				StartTime:        startTime.UnixMilli(),
				EndTime:          time.Now().UnixMilli(),
				Description:      params.Description,
				WorkingDirectory: bgShell.WorkingDir,
				Background:       true,
				ShellID:          bgShell.ID,
				BackgroundReason: reason.String(),
			}
			watchBackgroundJob(ctx, bgShell)
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(movedToBackgroundResponse(bgShell.ID, reason)), metadata), nil
		},
	)
}

// backgroundReason says why a foreground command was handed back to the
// model as a background job.
type backgroundReason int

const (
	// backgroundReasonTimeout: the auto_background_after threshold passed.
	backgroundReasonTimeout backgroundReason = iota
	// backgroundReasonSteer: a step-wide soft interrupt fired because a
	// user message is waiting to be folded into the turn.
	backgroundReasonSteer
	// backgroundReasonUser: the user backgrounded this specific command.
	backgroundReasonUser
)

// String returns the metadata label for the reason.
func (r backgroundReason) String() string {
	switch r {
	case backgroundReasonSteer:
		return "steer"
	case backgroundReasonUser:
		return "user"
	default:
		return "timeout"
	}
}

// movedToBackgroundResponse is the single tool result used whenever a
// foreground command keeps running as a background job, whichever path
// got it there. Only the first line differs so the model learns the same
// job-id / job_output / job_kill contract in every case.
func movedToBackgroundResponse(shellID string, reason backgroundReason) string {
	var lead string
	switch reason {
	case backgroundReasonSteer:
		lead = "Command is still running and has been moved to background early because a user message is waiting for you. Read and act on the user's message first."
	case backgroundReasonUser:
		lead = "Command is still running and has been moved to background by the user. Continue with other work; you will be notified when it finishes, so do not poll for it."
	default:
		lead = "Command is taking longer than expected and has been moved to background. You will be notified when it finishes."
	}
	return fmt.Sprintf("%s\n\nBackground shell ID: %s\n\nUse job_output tool to view output or job_kill to terminate.", lead, shellID)
}

// jobNotificationOutputLimit caps how much of a finished job's output is
// folded into the conversation; the model can fetch the rest with
// job_output.
const jobNotificationOutputLimit = 4000

// JobFinishedNoticePrefix opens the user-role aside folded into the
// conversation when a background job completes. The chat UI keys on it
// to render the message as a system notice instead of a user bubble.
const JobFinishedNoticePrefix = "[background job finished]"

// watchBackgroundJob waits for a job that stayed in the background to
// finish and reports the outcome to the session through the notifier on
// ctx (if any). It captures the notifier and session up front: the tool
// call's context is long gone by the time the job ends. A job that was
// killed (job_kill, shutdown) produces no notification — whoever killed
// it already knows.
func watchBackgroundJob(ctx context.Context, bgShell *shell.BackgroundShell) {
	notify := JobNotifier(ctx)
	if notify == nil {
		return
	}
	sessionID := GetSessionFromContext(ctx)
	go func() {
		bgShell.Wait()
		stdout, stderr, _, execErr := bgShell.GetOutput()
		if shell.IsInterrupt(execErr) {
			return
		}
		notify(sessionID, jobFinishedNotification(bgShell.ID, bgShell.Description, bgShell.Command, formatOutput(stdout, stderr, execErr)))
	}()
}

// jobFinishedNotification renders the aside folded into the conversation
// when a background job completes. It is persisted verbatim as a user
// message, so it is phrased to read as a system notice rather than
// something the user typed.
func jobFinishedNotification(shellID, description, command, output string) string {
	label := cmp.Or(description, DefaultBashDescription(command))
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (job %s)\n", JobFinishedNoticePrefix, label, shellID)
	fmt.Fprintf(&b, "Command: %s\n", strings.TrimSpace(command))
	if output == "" {
		output = BashNoOutput
	}
	if len(output) > jobNotificationOutputLimit {
		output = "…" + output[len(output)-jobNotificationOutputLimit:]
	}
	fmt.Fprintf(&b, "\n%s\n", output)
	b.WriteString("\nThis is an automatic notice that a command you moved to the background has completed. Use the result if it matters for what you are doing, otherwise carry on; the full output stays available via job_output for a while.")
	return b.String()
}

// DefaultBashDescription synthesizes a short label for a shell command
// when the model omitted the optional description parameter. It is the
// first non-empty line of the command, capped so it fits a tool header.
func DefaultBashDescription(command string) string {
	const maxRunes = 60
	for line := range strings.SplitSeq(command, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if r := []rune(line); len(r) > maxRunes {
			return string(r[:maxRunes]) + "…"
		}
		return line
	}
	return "shell command"
}

// formatOutput formats the output of a completed command with error handling
func formatOutput(stdout, stderr string, execErr error) string {
	interrupted := shell.IsInterrupt(execErr)
	exitCode := shell.ExitCode(execErr)

	stdout = truncateOutput(stdout)
	stderr = truncateOutput(stderr)

	errorMessage := stderr
	if errorMessage == "" && execErr != nil {
		errorMessage = execErr.Error()
	}

	if interrupted {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += "Command was aborted before completion"
	} else if exitCode != 0 {
		if errorMessage != "" {
			errorMessage += "\n"
		}
		errorMessage += fmt.Sprintf("Exit code %d", exitCode)
	}

	hasBothOutputs := stdout != "" && stderr != ""

	if hasBothOutputs {
		stdout += "\n"
	}

	if errorMessage != "" {
		// Only insert a separating newline when there is preceding stdout;
		// otherwise the result would start with a spurious blank line.
		if stdout != "" {
			stdout += "\n"
		}
		stdout += errorMessage
	}

	return stdout
}

func TruncateOutput(content string) string {
	if len(content) <= MaxOutputLength {
		return content
	}

	halfLength := MaxOutputLength / 2
	start := content[:halfLength]
	end := content[len(content)-halfLength:]

	truncatedLinesCount := countLines(content[halfLength : len(content)-halfLength])
	return fmt.Sprintf("%s\n\n... [%d lines truncated] ...\n\n%s", start, truncatedLinesCount, end)
}

func truncateOutput(content string) string {
	return TruncateOutput(content)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

func normalizeWorkingDir(path string) string {
	if runtime.GOOS == "windows" {
		path = strings.ReplaceAll(path, fsext.WindowsWorkingDirDrive(), "")
	}
	return filepath.ToSlash(path)
}
