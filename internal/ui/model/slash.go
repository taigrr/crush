package model

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/ui/completions"
	"github.com/taigrr/crush/internal/ui/util"
)

// slashCommand is a builtin command invoked by typing a /-prefixed verb at
// the chat prompt (e.g. "/export notes"). Builtin slash commands are
// distinct from the command palette (internal/ui/dialog) and from
// user-defined custom commands (internal/commands): they are quick,
// typed-inline actions that operate on the current session.
type slashCommand struct {
	// name is the canonical verb without the leading slash.
	name string
	// aliases are alternate verbs that resolve to this command.
	aliases []string
	// argHint documents the argument syntax for help and completion
	// (e.g. "[filename]"). Empty when the command takes no arguments.
	argHint string
	// description is a one-line summary for help and completion.
	description string
	// requiresSession gates the command on an active session. When true
	// and no session exists, dispatch reports a consistent error and run
	// is not called.
	requiresSession bool
	// run executes the command with the trimmed argument string.
	run func(m *UI, args string) tea.Cmd
}

// builtinSlashCommands is the registry of inline slash commands. It is the
// single source of truth for dispatch, help, and completion.
var builtinSlashCommands = []slashCommand{
	{
		name:            "btw",
		argHint:         "<message>",
		description:     "Fold an aside into the active turn",
		requiresSession: true,
		run: func(m *UI, args string) tea.Cmd {
			if args == "" {
				return util.ReportError(fmt.Errorf("/btw requires a message"))
			}
			return m.sendBTWMessage(args)
		},
	},
	{
		name:            "export",
		argHint:         "[filename]",
		description:     "Export the conversation to a Markdown file",
		requiresSession: true,
		run: func(m *UI, args string) tea.Cmd {
			return m.exportConversation(args)
		},
	},
	{
		name:            "continue",
		description:     "Resume the previous task as if the turn never ended",
		requiresSession: true,
		run: func(m *UI, _ string) tea.Cmd {
			return m.continueTurn()
		},
	},
	{
		name:            "goal",
		argHint:         "[condition | clear | (empty for status)]",
		description:     "Keep working autonomously until a condition is met",
		requiresSession: true,
		run: func(m *UI, args string) tea.Cmd {
			return m.handleGoal(args)
		},
	},
	{
		name:            "rename",
		argHint:         "[title | (empty for AI-generated title)]",
		description:     "Rename the session, or regenerate the title with AI when blank",
		requiresSession: true,
		run: func(m *UI, args string) tea.Cmd {
			return m.handleRename(args)
		},
	},
	{
		name:            "cwd",
		argHint:         "[path | (empty for terminal cwd)]",
		description:     "Set the working directory tools run in for this session",
		requiresSession: true,
		run: func(m *UI, args string) tea.Cmd {
			return m.handleCwd(args)
		},
	},
	{
		name:            "review",
		description:     "Run two adversarial reviewers in parallel on the current change",
		requiresSession: true,
		run: func(m *UI, _ string) tea.Cmd {
			// The `review` tool description and the coder prompt already
			// document how to pick the diff base and run the loop, so
			// this only needs to trigger it.
			return m.sendMessage("Review the current change with the `review` tool, then fix any real issues the reviewers surface.")
		},
	},
}

// splitSlash splits a trimmed prompt value into a slash verb and its
// argument string. ok is false when value is not a slash command (it does
// not begin with "/", or is just "/").
func splitSlash(value string) (verb, args string, ok bool) {
	if !strings.HasPrefix(value, "/") || value == "/" {
		return "", "", false
	}
	rest := value[1:]
	if v, a, found := strings.Cut(rest, " "); found {
		return v, strings.TrimSpace(a), true
	}
	return rest, "", true
}

// lookupSlash resolves a verb to a registered command by name or alias.
func lookupSlash(cmds []slashCommand, verb string) (slashCommand, bool) {
	for _, c := range cmds {
		if c.name == verb || slices.Contains(c.aliases, verb) {
			return c, true
		}
	}
	return slashCommand{}, false
}

// slashCommandCompletions projects the builtin registry into completion
// values for the inline completions popup.
func slashCommandCompletions() []completions.CommandCompletionValue {
	out := make([]completions.CommandCompletionValue, 0, len(builtinSlashCommands))
	for _, c := range builtinSlashCommands {
		out = append(out, completions.CommandCompletionValue{
			Name:        c.name,
			ArgHint:     c.argHint,
			Description: c.description,
		})
	}
	return out
}

// dispatchSlash routes value to a builtin slash command. handled is false
// when value is not a recognized slash command, in which case the caller
// treats value as a normal chat message. When handled is true the returned
// cmd (possibly nil) is the command's effect; cross-cutting concerns
// (session gating, prompt/history reset) are applied here so individual
// commands don't repeat them.
func (m *UI) dispatchSlash(value string) (cmd tea.Cmd, handled bool) {
	verb, args, ok := splitSlash(value)
	if !ok {
		return nil, false
	}
	c, found := lookupSlash(builtinSlashCommands, verb)
	if !found {
		return nil, false
	}
	if c.requiresSession && !m.hasSession() {
		return util.ReportError(fmt.Errorf("/%s requires an active session", c.name)), true
	}
	m.randomizePlaceholders()
	m.historyReset()
	return c.run(m, args), true
}
