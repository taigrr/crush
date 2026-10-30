package codesign

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type recorder struct {
	calls [][]string
	out   []byte
	err   error
}

func (r *recorder) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.out, r.err
}

func found(string) (string, error)    { return "/usr/bin/codesign", nil }
func notFound(string) (string, error) { return "", errors.New("not found") }

func TestSignDarwin(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	ok, err := Signer{GOOS: "darwin", LookPath: found, Run: r.run}.Sign(t.Context(), "/bin/crush")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, [][]string{{"/usr/bin/codesign", "-s", "-", "-i", "land.charm.crush", "-f", "/bin/crush"}}, r.calls)
}

func TestSignNonDarwin(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	ok, err := Signer{GOOS: "linux", LookPath: found, Run: r.run}.Sign(t.Context(), "/bin/crush")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, r.calls)
}

func TestSignNoCodesign(t *testing.T) {
	t.Parallel()
	r := &recorder{}
	ok, err := Signer{GOOS: "darwin", LookPath: notFound, Run: r.run}.Sign(t.Context(), "/bin/crush")
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, r.calls)
}

func TestSignFailure(t *testing.T) {
	t.Parallel()
	r := &recorder{out: []byte("boom\n"), err: errors.New("exit 1")}
	ok, err := Signer{GOOS: "darwin", LookPath: found, Run: r.run}.Sign(t.Context(), "/bin/crush")
	require.False(t, ok)
	require.ErrorContains(t, err, "boom")
}
