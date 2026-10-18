package headless

import (
	"errors"
	"fmt"
)

// Process exit codes for headless commands. They are part of the public
// contract: wrappers branch on them, so existing values must never be
// renumbered.
const (
	// ExitOK means the run completed.
	ExitOK = 0
	// ExitFailure is a generic failure before or around the run (bad
	// flags, server unreachable, no providers configured, ...).
	ExitFailure = 1
	// ExitCancelled means the run was cancelled before it finished.
	ExitCancelled = 4
	// ExitAgentError means the agent run itself failed (provider error,
	// tool loop failure, ...).
	ExitAgentError = 5
)

// ExitError carries a process exit code alongside the underlying error.
// When Silent is set the error has already been reported in the chosen
// output format and the CLI layer should not print it again.
type ExitError struct {
	Code   int
	Err    error
	Silent bool
}

// Error implements the error interface.
func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

// Unwrap returns the underlying error.
func (e *ExitError) Unwrap() error { return e.Err }

// NewExitError wraps err with the given exit code.
func NewExitError(code int, err error) *ExitError {
	return &ExitError{Code: code, Err: err}
}

// ExitCode maps err to the process exit code: 0 for nil, the embedded
// code for an [ExitError], and [ExitFailure] for anything else.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return ExitFailure
}

// IsSilent reports whether err has already been reported to the user in
// the selected output format.
func IsSilent(err error) bool {
	var exitErr *ExitError
	return errors.As(err, &exitErr) && exitErr.Silent
}
