package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContainsCommandChaining(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"plain ls", "ls -la", false},
		{"plain echo", "echo hello world", false},
		{"plain pwd", "pwd", false},
		{"plain git status", "git status", false},
		{"ls with redirect", "ls > /tmp/out", false},
		{"ls with pipe", "ls | grep foo", true},
		{"ls with double ampersand", "ls && echo done", true},
		{"ls with semicolon", "ls; echo done", true},
		{"ls with pipe pipe", "ls || echo fail", true},
		{"ls with backticks", "ls `echo foo`", true},
		{"ls with subshell", "ls $(echo foo)", true},
		{"ls with background ampersand", "ls & echo done", false},
		{"rm -rf with && ls (rm first)", "rm -rf / && ls", true},
		{"redirect with ampersand gt", "ls &> /dev/null", false},
		{"redirect with gt ampersand", "ls >& /dev/null", false},
		{"simple kill", "kill 1234", false},
		{"kill with pipe", "kill 1234 | echo foo", true},
		{"git log", "git log --oneline", false},
		{"git log with pipe", "git log | head", true},
		{"empty string", "", false},
		{"dollar sign in argument", "echo $HOME", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := containsCommandChaining(tt.input)
			assert.Equal(t, tt.expected, got, "containsCommandChaining(%q)", tt.input)
		})
	}
}

func TestIsSafeReadOnlyCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"plain ls", "ls", true},
		{"ls with flags", "ls -la", true},
		{"ls with path", "ls /tmp", true},
		{"git status", "git status", true},
		{"git log with flags", "git log --oneline", true},
		{"uppercase normalized", "LS -LA", true},

		// Word-boundary: prefixes of safe commands must not match.
		{"lscpu not ls", "lscpu", false},
		{"idle not id", "idle", false},

		// Not in safe list.
		{"rm is not safe", "rm -rf /tmp/x", false},
		{"cat is not safe", "cat secrets", false},

		// Chaining / substitution disqualifies.
		{"ls piped", "ls | grep foo", false},
		{"ls and echo", "ls && echo done", false},
		{"ls semicolon", "ls; rm x", false},
		{"ls subshell", "ls $(whoami)", false},

		// Redirection / background can mutate state: must prompt.
		{"echo redirect overwrites file", "echo pwned > /etc/passwd", false},
		{"ls append redirect", "ls >> out.txt", false},
		{"ls background", "ls &", false},
		{"ls redirect both", "ls &> /dev/null", false},

		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isSafeReadOnlyCommand(tt.input)
			assert.Equal(t, tt.expected, got, "isSafeReadOnlyCommand(%q)", tt.input)
		})
	}
}
