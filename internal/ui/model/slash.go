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
	// argCompletions, when set, supplies completions for the next argument
	// given the arguments typed so far (trimmed, possibly empty). The popup
	// opens each time the user types a space after the verb or after a
	// completed argument. nil means the command offers no argument
	// completion.
	argCompletions func(m *UI, args string) []completions.ArgCompletionValue
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
		name:            "bg",
		aliases:         []string{"background"},
		description:     "Move the running tools to the background and let the turn continue",
		requiresSession: true,
		run: func(m *UI, _ string) tea.Cmd {
			return m.softInterruptTurn()
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
		name:            "fork",
		argHint:         "[message]",
		description:     "Fork this conversation into a new session (at the latest message, or the one chosen)",
		requiresSession: true,
		run: func(m *UI, args string) tea.Cmd {
			return m.handleForkSlash(args)
		},
		argCompletions: func(m *UI, args string) []completions.ArgCompletionValue {
			return m.forkArgCompletions(args)
		},
	},
	{
		name:            "model",
		argHint:         "[role] [model [effort]]",
		description:     "Switch the large model, or set a role (large, small, worker, custom) for the workspace",
		requiresSession: false,
		run: func(m *UI, args string) tea.Cmd {
			return m.handleModelSlash(args)
		},
		argCompletions: func(m *UI, args string) []completions.ArgCompletionValue {
			return m.modelArgCompletions(args)
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
	{
		name:        "mcp-auth",
		argHint:     "[server | (empty for all pending)]",
		description: "Authenticate an OAuth MCP server via the browser",
		run: func(m *UI, args string) tea.Cmd {
			return m.handleMCPAuth(args)
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

// slashArgCompletions returns argument completions for the slash command
// being typed in value, or nil when value is not a slash command with
// argument completion. value is expected to end just after a space.
func (m *UI) slashArgCompletions(value string) []completions.ArgCompletionValue {
	if strings.ContainsAny(value, "\n") {
		return nil
	}
	verb, args, ok := splitSlash(strings.TrimRight(value, " "))
	if !ok {
		return nil
	}
	c, found := lookupSlash(builtinSlashCommands, verb)
	if !found || c.argCompletions == nil {
		return nil
	}
	return c.argCompletions(m, args)
}

// dispatchSlash routes value to a builtin slash command. handled is false
// when value is not a recognized slash command, in which case the caller
// treats value as a normal chat message. consume is false when dispatch
// rejects the command, so the caller can preserve the user's input.
func (m *UI) dispatchSlash(value string) (cmd tea.Cmd, handled, consume bool) {
	verb, args, ok := splitSlash(value)
	if !ok {
		return nil, false, false
	}
	c, found := lookupSlash(builtinSlashCommands, verb)
	if !found {
		return nil, false, false
	}
	if c.requiresSession && !m.hasSession() {
		return util.ReportError(fmt.Errorf("/%s requires an active session", c.name)), true, false
	}
	m.randomizePlaceholders()
	m.historyReset()
	return c.run(m, args), true, true
}
