package tools

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

const gitIdentityStderr = `Author identity unknown

*** Please tell me who you are.

Run

  git config --global user.email "you@example.com"
  git config --global user.name "Your Name"

to set your account's default identity.
Omit --global to set the identity only in this repository.

fatal: unable to auto-detect email address (got 'me@host.(none)')`

func TestFormatOutputGitIdentityMissing(t *testing.T) {
	t.Parallel()

	out := formatOutput("", gitIdentityStderr, errors.New("exit status 128"))
	require.Contains(t, out, gitIdentityStderr)
	require.Contains(t, out, GitIdentityMissingNotice)
	require.Contains(t, out, "Do NOT retry")
}

func TestFormatOutputGitIdentityIgnoredOnSuccess(t *testing.T) {
	t.Parallel()

	out := formatOutput("Please tell me who you are", "", nil)
	require.NotContains(t, out, GitIdentityMissingNotice)
}

func TestFormatOutputNoGitIdentityNoticeForOtherErrors(t *testing.T) {
	t.Parallel()

	out := formatOutput("", "fatal: not a git repository", errors.New("exit status 128"))
	require.NotContains(t, out, GitIdentityMissingNotice)
}

func TestIsGitIdentityError(t *testing.T) {
	t.Parallel()

	require.True(t, isGitIdentityError("*** Please tell me who you are."))
	require.True(t, isGitIdentityError("fatal: unable to auto-detect email address (got 'x')"))
	require.True(t, isGitIdentityError("fatal: empty ident name (for <a@b>) not allowed"))
	require.False(t, isGitIdentityError("nothing to commit, working tree clean"))
}
