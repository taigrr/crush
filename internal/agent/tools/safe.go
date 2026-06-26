package tools

import (
	"runtime"
	"slices"
	"strings"
)

var safeCommands = []string{
	// Bash builtins and core utils
	"cal",
	"date",
	"df",
	"du",
	"echo",
	"env",
	"free",
	"groups",
	"hostname",
	"id",
	"kill",
	"killall",
	"ls",
	"nice",
	"nohup",
	"printenv",
	"ps",
	"pwd",
	"set",
	"time",
	"timeout",
	"top",
	"type",
	"uname",
	"unset",
	"uptime",
	"whatis",
	"whereis",
	"which",
	"whoami",

	// Git
	"git blame",
	"git branch",
	"git config --get",
	"git config --list",
	"git describe",
	"git diff",
	"git grep",
	"git log",
	"git ls-files",
	"git ls-remote",
	"git remote",
	"git rev-parse",
	"git shortlog",
	"git show",
	"git status",
	"git tag",
}

var chainingMetacharacters = []string{
	";",
	"|",
	"&&",
	"$(",
	"`",
}

// containsCommandChaining reports whether s contains shell metacharacters
// that enable command chaining or substitution.
func containsCommandChaining(s string) bool {
	return slices.ContainsFunc(chainingMetacharacters, func(c string) bool {
		return strings.Contains(s, c)
	})
}

// isSafeReadOnlyCommand reports whether command can be executed without a
// permission prompt because it is a known read-only command with no way to
// mutate state.
//
// A command qualifies only when it does not chain or substitute other
// commands (containsCommandChaining), does not redirect output to a file or
// run in the background (which could create or overwrite files), and begins
// with one of safeCommands at a word boundary so that e.g. "lscpu" does not
// match the "ls" prefix.
func isSafeReadOnlyCommand(command string) bool {
	if containsCommandChaining(command) {
		return false
	}
	// Output redirection (">", ">>") can write files and "&" can background
	// the process; both fall outside "read-only".
	if strings.ContainsAny(command, ">&") {
		return false
	}

	cmdLower := strings.ToLower(command)
	for _, safe := range safeCommands {
		if !strings.HasPrefix(cmdLower, safe) {
			continue
		}
		// Require a word boundary after the safe command so prefixes like
		// "ls" do not match "lscpu".
		if len(cmdLower) == len(safe) || cmdLower[len(safe)] == ' ' || cmdLower[len(safe)] == '-' {
			return true
		}
	}
	return false
}

func init() {
	if runtime.GOOS == "windows" {
		safeCommands = append(
			safeCommands,
			// Windows-specific commands
			"ipconfig",
			"nslookup",
			"ping",
			"systeminfo",
			"tasklist",
			"where",
		)
	}
}
