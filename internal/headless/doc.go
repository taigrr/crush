// Package headless implements the machine-readable contract used when
// Crush is driven by another program (a script, CI job, or a different
// coding agent such as Claude Code) instead of a human at the TUI.
//
// It owns the output formats accepted by `crush run --output-format`,
// the newline-delimited event schema emitted in stream-json mode, the
// final [Result] object, and the process exit codes that let callers
// tell a completed run apart from a cancelled or failed one.
package headless
