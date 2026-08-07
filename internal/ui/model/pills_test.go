package model

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/ui/common"
)

func TestQueuePillArrowReflectsExpandState(t *testing.T) {
	t.Parallel()
	com := common.DefaultCommon(nil)
	t2 := com.Styles

	collapsed := ansi.Strip(queuePill(1, false, false, t2))
	require.Contains(t, collapsed, "▶")
	require.NotContains(t, collapsed, "▼")

	expanded := ansi.Strip(queuePill(1, true, true, t2))
	require.Contains(t, expanded, "▼")
	require.NotContains(t, expanded, "▶")
}
