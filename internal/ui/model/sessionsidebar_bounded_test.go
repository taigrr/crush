package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSidebar_BoundedOverviewCountsHiddenSessions verifies sessions the
// server left out of a bounded overview still show in the "…N more" row
// and the total tally.
func TestSidebar_BoundedOverviewCountsHiddenSessions(t *testing.T) {
	t.Parallel()
	s := newTestSidebar(t)
	ovs := sampleOverviews()
	ovs[0].SessionCount = 10
	s.SetOverviews(ovs)

	require.True(t, s.Truncated())
	require.Equal(t, 11, s.SessionCounts().Total)
	var overflow *sidebarRow
	for i := range s.rows {
		if s.rows[i].kind == sidebarRowOverflow {
			overflow = &s.rows[i]
		}
	}
	require.NotNil(t, overflow)
	require.Equal(t, 0, overflow.wsIdx)
	require.Equal(t, 8, overflow.remaining)

	full := sampleOverviews()
	s.SetOverviews(full)
	require.False(t, s.Truncated())
	require.Equal(t, 3, s.SessionCounts().Total)
}
