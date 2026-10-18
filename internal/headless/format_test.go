package headless

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseOutputFormat(t *testing.T) {
	t.Parallel()

	cases := map[string]OutputFormat{
		"":            FormatText,
		"text":        FormatText,
		"JSON":        FormatJSON,
		"stream-json": FormatStreamJSON,
		"Stream-Json": FormatStreamJSON,
	}
	for in, want := range cases {
		got, err := ParseOutputFormat(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}

	_, err := ParseOutputFormat("yaml")
	require.ErrorContains(t, err, `invalid output format "yaml"`)
	require.ErrorContains(t, err, "text, json, stream-json")
}

func TestOutputFormatIsStructured(t *testing.T) {
	t.Parallel()
	require.False(t, FormatText.IsStructured())
	require.True(t, FormatJSON.IsStructured())
	require.True(t, FormatStreamJSON.IsStructured())
}

func TestExitCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, ExitOK, ExitCode(nil))
	require.Equal(t, ExitFailure, ExitCode(errors.New("boom")))
	require.Equal(t, ExitCancelled, ExitCode(NewExitError(ExitCancelled, errors.New("x"))))

	wrapped := fmt.Errorf("outer: %w", NewExitError(ExitAgentError, errors.New("inner")))
	require.Equal(t, ExitAgentError, ExitCode(wrapped))
	require.Equal(t, "outer: inner", wrapped.Error())
}

func TestExitErrorMessageAndUnwrap(t *testing.T) {
	t.Parallel()

	inner := errors.New("inner")
	e := NewExitError(ExitAgentError, inner)
	require.ErrorIs(t, e, inner)
	require.Equal(t, "inner", e.Error())
	require.Equal(t, "exit status 4", (&ExitError{Code: ExitCancelled}).Error())
}

func TestIsSilent(t *testing.T) {
	t.Parallel()

	require.False(t, IsSilent(nil))
	require.False(t, IsSilent(errors.New("x")))
	require.False(t, IsSilent(NewExitError(1, errors.New("x"))))
	require.True(t, IsSilent(fmt.Errorf("w: %w", &ExitError{Code: 5, Silent: true})))
}

func TestStatusExitCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, ExitOK, StatusCompleted.ExitCode())
	require.Equal(t, ExitCancelled, StatusCancelled.ExitCode())
	require.Equal(t, ExitAgentError, StatusError.ExitCode())
	require.Equal(t, ExitFailure, Status("weird").ExitCode())
}

func TestResultErr(t *testing.T) {
	t.Parallel()

	require.NoError(t, NewResult(StatusCompleted).Err())

	r := NewResult(StatusError)
	r.Error = "provider exploded"
	err := r.Err()
	require.EqualError(t, err, "provider exploded")
	require.Equal(t, ExitAgentError, ExitCode(err))
	require.True(t, IsSilent(err))

	err = NewResult(StatusCancelled).Err()
	require.EqualError(t, err, "cancelled")
	require.Equal(t, ExitCancelled, ExitCode(err))
}

func TestNewResultDefaults(t *testing.T) {
	t.Parallel()

	r := NewResult(StatusCompleted)
	require.Equal(t, EventResult, r.Type)
	require.NotNil(t, r.FilesChanged, "files_changed must serialize as [] rather than null")
}
