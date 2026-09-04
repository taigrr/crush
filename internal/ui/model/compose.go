package model

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/agent/tools"
	"github.com/taigrr/crush/internal/home"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/version"
	"github.com/taigrr/crush/internal/workspace"
)

// attachSkill reads a skill's content by ID and returns it as a markdown
// attachment to be added to the attachment toolbar. The user can then
// compose a message and send it with the skill attached.
// The name parameter is used as a fallback when the server does not
// return one.
func (m *UI) attachSkill(skillID, name string) tea.Cmd {
	return func() tea.Msg {
		content, result, err := m.com.Workspace.ReadSkill(context.Background(), skillID)
		if err != nil {
			return util.NewErrorMsg(err)
		}
		fileName := result.Name
		if fileName == "" {
			fileName = name
		}
		return message.Attachment{
			FilePath: fileName,
			FileName: fileName,
			MimeType: "text/markdown",
			Content:  content,
		}
	}
}

// restoreUnsentPrompt puts a prompt back into the editor after a failed
// send so the user's input is never lost. It restores the text and any
// attachments, then reports the error that prevented sending.
func (m *UI) restoreUnsentPrompt(content string, attachments []message.Attachment, err error) tea.Cmd {
	prevHeight := m.textarea.Height()
	content = strings.TrimPrefix(content, "[btw] ")
	switch existing := m.textarea.Value(); {
	case existing == "":
		m.textarea.SetValue(content)
	case content != "" && !strings.Contains(existing, content):
		// Several prompts can come back at once (held during a server
		// update and rejected on redelivery); keep them all rather than
		// the first only.
		m.textarea.SetValue(existing + "\n\n" + content)
	}
	for _, att := range attachments {
		m.attachments.Update(att)
	}
	cmds := []tea.Cmd{util.ReportError(err)}
	if cmd := m.handleTextareaHeightChange(prevHeight); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// sendMessage sends a message with the given content and attachments.
func (m *UI) sendMessage(content string, attachments ...message.Attachment) tea.Cmd {
	readyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ready, err := m.com.Workspace.AgentReadiness(readyCtx)
	switch {
	case err != nil:
		// Could not reach the server (e.g. spotty network). This is NOT
		// the "agent not initialized" case: do not clear the prompt, and
		// report a transient error the user can simply retry.
		return m.restoreUnsentPrompt(content, attachments,
			fmt.Errorf("could not reach crush server, please try again: %w", err))
	case !ready:
		// A version mismatch is unrecoverable here: the shared server
		// speaks a different protocol. Preserve the prompt and tell the
		// user to restart rather than silently dropping their input.
		if m.versionMismatch {
			return m.restoreUnsentPrompt(content, attachments,
				fmt.Errorf("crush server version (%s) does not match this client (%s); restart crush", m.serverVersionStr, version.Version))
		}
		// Otherwise the agent genuinely failed to initialize (the server
		// is reachable but reports not-ready). Try once to (re)initialize
		// it so the user's carefully typed prompt is not lost.
		if initErr := m.com.Workspace.InitCoderAgent(readyCtx); initErr != nil {
			return m.restoreUnsentPrompt(content, attachments,
				fmt.Errorf("coder agent is not initialized: %w", initErr))
		}
		if ok, rerr := m.com.Workspace.AgentReadiness(readyCtx); rerr != nil || !ok {
			return m.restoreUnsentPrompt(content, attachments,
				fmt.Errorf("coder agent is not initialized"))
		}
	}

	var cmds []tea.Cmd
	if !m.hasSession() {
		newSession, err := m.com.Workspace.CreateSession(context.Background(), "New Session")
		if err != nil {
			return util.ReportError(err)
		}
		if m.forceCompactMode {
			m.isCompact = true
		}
		if newSession.ID != "" {
			m.session = &newSession
			cmds = append(cmds, m.loadSession(newSession.ID))
		}
		m.setState(uiChat, m.focus)
	}

	ctx := context.Background()
	cmds = append(cmds, func() tea.Msg {
		for _, path := range m.sessionFileReads {
			m.com.Workspace.FileTrackerRecordRead(ctx, m.session.ID, path)
			m.com.Workspace.LSPStart(ctx, path)
		}
		return nil
	})

	// Capture session ID to avoid race with main goroutine updating m.session.
	sessionID := m.session.ID
	cmds = append(cmds, func() tea.Msg {
		// AgentRun is fire-and-forget: it returns once the prompt has
		// been accepted (HTTP 202) or synchronously with a validation
		// or transport error. Run failures and cancellation surface
		// through SSE-derived events, not this return value.
		err := m.com.Workspace.AgentRun(context.Background(), sessionID, content, attachments...)
		if errors.Is(err, workspace.ErrServerUpdating) {
			// Not a failure: the server is being swapped for a newer
			// build and the workspace is holding the prompt until it
			// reconnects. Say so instead of alarming the user.
			return util.InfoMsg{
				Type: util.InfoTypeInfo,
				Msg:  "Server is updating — your message is held and will be sent when it reconnects (keep this window open).",
			}
		}
		if err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("%v", err),
			}
		}
		return nil
	})
	return tea.Batch(cmds...)
}

// handleCwd sets the working directory tools run in for the current
// session. With no argument it uses the client's terminal cwd; with an
// argument it resolves the path (relative paths are resolved against the
// terminal cwd). The change is persisted server-side and, when the agent
// is busy, an aside is folded into the active turn so the model is told
// its cwd changed and does not try to cd into it.
func (m *UI) handleCwd(args string) tea.Cmd {
	terminalCwd, err := os.Getwd()
	if err != nil {
		return util.ReportError(fmt.Errorf("cannot determine terminal working directory: %w", err))
	}

	target := terminalCwd
	if args != "" {
		p := home.Long(strings.TrimSpace(args))
		if !filepath.IsAbs(p) {
			p = filepath.Join(terminalCwd, p)
		}
		target = filepath.Clean(p)
	}

	info, err := os.Stat(target)
	if err != nil {
		return util.ReportError(fmt.Errorf("cannot use %q: %w", target, err))
	}
	if !info.IsDir() {
		return util.ReportError(fmt.Errorf("%q is not a directory", target))
	}

	sessionID := m.session.ID
	busy := m.isAgentBusy()
	return tea.Batch(
		func() tea.Msg {
			if err := m.com.Workspace.AgentSetWorkingDir(sessionID, target); err != nil {
				return util.InfoMsg{Type: util.InfoTypeError, Msg: fmt.Sprintf("%v", err)}
			}
			return nil
		},
		func() tea.Msg {
			// Inform the model only when it is mid-turn; the aside folds
			// into the active step without hurrying it (a cwd change is
			// not worth backgrounding a running command). When idle, the
			// next turn's system prompt carries the new cwd (see run.go
			// environment block), so no wasteful turn is triggered here.
			if busy {
				_ = m.com.Workspace.AgentRunAside(context.Background(), sessionID,
					"The working directory is now "+target+". Treat relative paths as relative to it; do not cd into it.")
			}
			return nil
		},
		util.ReportInfo("Working directory set to "+home.Short(target)),
	)
}

// handleRename renames the current session. With an argument it sets the
// title directly; with no argument it triggers an AI-generated title based
// on the conversation so far. The caller (dispatchSlash) guarantees an
// active session.
func (m *UI) handleRename(args string) tea.Cmd {
	sessionID := m.session.ID
	title := strings.TrimSpace(args)
	if title != "" {
		sess := *m.session
		sess.Title = title
		return tea.Batch(
			func() tea.Msg {
				if _, err := m.com.Workspace.SaveSession(context.Background(), sess); err != nil {
					return util.InfoMsg{Type: util.InfoTypeError, Msg: fmt.Sprintf("%v", err)}
				}
				return nil
			},
			util.ReportInfo("Session renamed to "+title),
		)
	}
	return tea.Batch(
		func() tea.Msg {
			if err := m.com.Workspace.AgentGenerateTitle(context.Background(), sessionID); err != nil {
				return util.InfoMsg{Type: util.InfoTypeError, Msg: fmt.Sprintf("%v", err)}
			}
			return nil
		},
		util.ReportInfo("Generating a new title…"),
	)
}

// sendBTWMessage sends a "by the way" aside that is folded into the active
// turn at the next step boundary rather than queued for its own turn.
func (m *UI) sendBTWMessage(content string) tea.Cmd {
	sessionID := m.session.ID
	return func() tea.Msg {
		err := m.com.Workspace.AgentRunBTW(context.Background(), sessionID, content)
		if err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("%v", err),
			}
		}
		return nil
	}
}

// steerOrSend routes a prompt submitted with the steer key. While the
// agent is busy the prompt is folded into the active turn as an aside;
// otherwise there is nothing to steer and it is sent as a normal turn.
// Steers are text-only: a prompt carrying attachments is queued as its
// own turn instead, and the user is told why.
func (m *UI) steerOrSend(content string, attachments ...message.Attachment) tea.Cmd {
	if !m.hasSession() || !m.isAgentBusy() {
		return m.sendMessage(content, attachments...)
	}
	if len(attachments) > 0 {
		return tea.Batch(
			util.ReportWarn("Steering does not support attachments; queued as a new turn instead"),
			m.sendMessage(content, attachments...),
		)
	}
	if content == "" {
		return nil
	}
	return m.sendBTWMessage(content)
}

// backgroundRunningBash asks the server to move the session's in-flight
// bash command to the background. The tool returns a job id and the turn
// carries on; the UI re-renders the call as a job once the result lands.
// Returns nil when no bash command is running, so the caller can let the
// key fall through to whatever else is bound to it.
func (m *UI) backgroundRunningBash() tea.Cmd {
	if !m.hasSession() || !m.isAgentBusy() {
		return nil
	}
	tc, ok := m.chat.RunningToolCall(tools.BashToolName)
	if !ok {
		return nil
	}
	sessionID := m.session.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.com.Workspace.AgentBackgroundTool(ctx, sessionID, tc.ID); err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("could not background command: %v", err),
			}
		}
		return util.InfoMsg{
			Type: util.InfoTypeInfo,
			Msg:  "Command moved to the background",
		}
	}
}

// softInterruptTurn asks every long-running tool in the session's current
// step to wrap up early (backgrounding shells, returning partial output)
// without cancelling the turn. Used by /bg.
func (m *UI) softInterruptTurn() tea.Cmd {
	if !m.isAgentBusy() {
		return util.ReportWarn("Agent is idle; nothing to background")
	}
	sessionID := m.session.ID
	return func() tea.Msg {
		m.com.Workspace.AgentSoftInterrupt(sessionID)
		return util.InfoMsg{
			Type: util.InfoTypeInfo,
			Msg:  "Asked running tools to wrap up and continue in the background",
		}
	}
}

// continueTurn re-kicks an idle session, instructing the model to resume
// the previous task as if its last turn never ended. The agent loop
// requires a triggering prompt, so a minimal continuation directive is
// sent; the model still has full conversation context and picks up where
// it left off. The caller (dispatchSlash) guarantees an active session.
func (m *UI) continueTurn() tea.Cmd {
	if m.isAgentBusy() {
		return util.ReportWarn("Agent is still working; nothing to continue")
	}
	const continuePrompt = "[continue] Resume the previous task and keep working from where you left off, as if your last turn never ended. Do not wait for further instructions."
	sessionID := m.session.ID
	return func() tea.Msg {
		if err := m.com.Workspace.AgentRun(context.Background(), sessionID, continuePrompt); err != nil {
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  fmt.Sprintf("%v", err),
			}
		}
		return nil
	}
}

// runShellCommand executes a shell command server-side without triggering
// the LLM. The command and output are persisted as a user message.
func (m *UI) runShellCommand(command string) tea.Cmd {
	var cmds []tea.Cmd
	if !m.hasSession() {
		newSession, err := m.com.Workspace.CreateSession(context.Background(), "New Session")
		if err != nil {
			return util.ReportError(err)
		}
		if m.forceCompactMode {
			m.isCompact = true
		}
		if newSession.ID != "" {
			m.session = &newSession
			cmds = append(cmds, m.loadSession(newSession.ID))
		}
		m.setState(uiChat, m.focus)
	}
	sessionID := m.session.ID
	// Run under a cancellable context so the Cancel key (esc/ctrl+c) can
	// abort a long-running command. Cancelling the client HTTP request
	// propagates to the server's shell.Run via its request context.
	ctx, cancel := context.WithCancel(context.Background())
	m.shellCancel = cancel
	cmds = append(cmds, func() tea.Msg {
		// Release the context when the command returns; cancelling twice is
		// harmless, so the Cancel key can still abort it mid-run.
		defer cancel()
		_, err := m.com.Workspace.AgentRunShellCommand(ctx, sessionID, command)
		return shellCommandFinishedMsg{err: err}
	})
	return tea.Batch(cmds...)
}
