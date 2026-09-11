package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/shell"
	"github.com/taigrr/fantasy"
)

const (
	JobOutputToolName = "job_output"
)

//go:embed job_output.md
var jobOutputDescription string

type JobOutputParams struct {
	ShellID string `json:"shell_id" description:"The ID of the background shell to retrieve output from"`
	Wait    bool   `json:"wait" description:"If true, block until the background shell completes before returning output"`
}

type JobOutputResponseMetadata struct {
	ShellID          string `json:"shell_id"`
	Command          string `json:"command"`
	Description      string `json:"description"`
	Done             bool   `json:"done"`
	WorkingDirectory string `json:"working_directory"`
}

func NewJobOutputTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		JobOutputToolName,
		jobOutputDescription,
		func(ctx context.Context, params JobOutputParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.ShellID == "" {
				return fantasy.NewTextErrorResponse("missing shell_id"), nil
			}

			bgManager := shell.GetBackgroundShellManager()
			bgShell, ok := bgManager.Get(params.ShellID)
			if !ok {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("background shell not found: %s", params.ShellID)), nil
			}

			var interrupted backgroundReason
			waitCut := false
			if params.Wait {
				// Block until the job finishes, but let a steer (step-wide
				// soft interrupt) or a per-call background request end the
				// wait early: the job keeps running and the model gets the
				// output so far with a "running" status.
				interrupted, waitCut = waitForJob(ctx, bgShell, call.ID)
			}

			stdout, stderr, done, err := bgShell.GetOutput()

			var outputParts []string
			if stdout != "" {
				outputParts = append(outputParts, stdout)
			}
			if stderr != "" {
				outputParts = append(outputParts, stderr)
			}

			status := "running"
			if done {
				status = "completed"
				if err != nil {
					exitCode := shell.ExitCode(err)
					if exitCode != 0 {
						outputParts = append(outputParts, fmt.Sprintf("Exit code %d", exitCode))
					}
				}
			}

			output := strings.Join(outputParts, "\n")
			output = TruncateOutput(output)

			metadata := JobOutputResponseMetadata{
				ShellID:          params.ShellID,
				Command:          bgShell.Command,
				Description:      bgShell.Description,
				Done:             done,
				WorkingDirectory: bgShell.WorkingDir,
			}

			if output == "" {
				output = BashNoOutput
			}

			result := fmt.Sprintf("Status: %s\n\n%s", status, output)
			if waitCut && !done {
				result = waitEndedEarlyNote(interrupted) + "\n\n" + result
			}
			return fantasy.WithResponseMetadata(fantasy.NewTextResponse(result), metadata), nil
		},
	)
}

// waitForJob blocks until bgShell finishes, the step is soft-interrupted,
// this call is asked to background, or ctx ends. It reports whether the
// wait was cut short and why. The backgroundable registration is released
// on every exit path.
func waitForJob(ctx context.Context, bgShell *shell.BackgroundShell, callID string) (reason backgroundReason, cut bool) {
	bgRequested, releaseBg := RegisterBackgroundable(ctx, callID)
	defer releaseBg()
	select {
	case <-bgShell.Done():
	case <-SoftInterrupt(ctx):
		return backgroundReasonSteer, true
	case <-bgRequested:
		return backgroundReasonUser, true
	case <-ctx.Done():
	}
	return backgroundReasonTimeout, false
}

// waitEndedEarlyNote explains why a job_output wait returned before the
// job finished; the job itself keeps running.
func waitEndedEarlyNote(reason backgroundReason) string {
	switch reason {
	case backgroundReasonUser:
		return "Stopped waiting at the user's request; the job is still running in the background."
	default:
		return "Stopped waiting because a user message is waiting for you; the job is still running in the background. Read and act on the user's message first."
	}
}
