//go:build linux

package voice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateRecordersOrder(t *testing.T) {
	t.Parallel()
	available := func(name string) bool {
		return name == "pw-record" || name == "parec" || name == "arecord"
	}
	got := candidateRecorders(available, func() bool { return true })
	require.Equal(t, []recorder{recPwRecord, recParec, recArecord}, got)

	got = candidateRecorders(available, func() bool { return false })
	require.Equal(t, []recorder{recParec, recArecord, recPwRecord}, got)
}
