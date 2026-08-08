package chat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/ui/styles"
)

// finishedInfoMessage builds a finished assistant message whose
// finish part carries the given Unix finish time.
func finishedInfoMessage(id string, finishUnix int64) *message.Message {
	return &message.Message{
		ID:   id,
		Role: message.Assistant,
		Parts: []message.ContentPart{
			message.TextContent{Text: "answer"},
			message.Finish{
				Reason: message.FinishReasonEndTurn,
				Time:   finishUnix,
			},
		},
	}
}

func testConfig() *config.Config {
	return &config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}
}

// TestHumanizeSince_AdvancesWithNow asserts the relative label recomputes
// as the injected clock advances past humanize granularity boundaries.
func TestHumanizeSince_AdvancesWithNow(t *testing.T) {
	t.Parallel()
	finish := time.Unix(testFinishTime, 0)

	justNow := humanizeSince(finish, finish.Add(2*time.Second))
	later := humanizeSince(finish, finish.Add(5*time.Minute))
	muchLater := humanizeSince(finish, finish.Add(3*time.Hour))

	require.NotEqual(t, justNow, later, "label must change after minutes elapse")
	require.NotEqual(t, later, muchLater, "label must change after hours elapse")
}

// TestAssistantInfo_RefreshSinceUpdatesRender is the core regression: the
// humanized "since" portion must recompute (and bypass the stale cache)
// when time advances, while returning false when the label is unchanged.
func TestAssistantInfo_RefreshSinceUpdatesRender(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := finishedInfoMessage("info1", time.Now().Unix())
	item := NewAssistantInfoItem(&sty, msg, testConfig(), time.Now()).(*AssistantInfoItem)

	const width = 71
	first := item.Render(width)
	require.NotEmpty(t, first)
	v0 := item.Version()

	// Advancing far into the future changes the humanized label.
	finish := time.Unix(msg.FinishPart().Time, 0)
	changed := item.RefreshSince(finish.Add(5 * time.Hour))
	require.True(t, changed, "label must change after hours elapse")
	require.Greater(t, item.Version(), v0, "version must bump so the list memo invalidates")

	second := item.Render(width)
	require.NotEqual(t, first, second,
		"cached render must be bypassed so the fresh since label shows")
	require.Contains(t, second, "hours ago")
}

// TestAssistantInfo_RefreshSinceNoChangeIsStable asserts that a second
// refresh at the same instant neither bumps the version nor invalidates
// the cache, so the ticker is cheap when nothing needs repainting.
func TestAssistantInfo_RefreshSinceNoChangeIsStable(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()
	msg := finishedInfoMessage("info2", time.Now().Unix())
	item := NewAssistantInfoItem(&sty, msg, testConfig(), time.Now()).(*AssistantInfoItem)

	const width = 71
	_ = item.Render(width)

	finish := time.Unix(msg.FinishPart().Time, 0)
	at := finish.Add(5 * time.Hour)

	require.True(t, item.RefreshSince(at), "first refresh at a new instant must change")
	rendered := item.Render(width)
	v := item.Version()

	// Refreshing again at the exact same instant must be a no-op.
	require.False(t, item.RefreshSince(at), "identical label must not report a change")
	require.Equal(t, v, item.Version(), "no bump when label is unchanged")
	require.Equal(t, rendered, item.Render(width), "cache must still serve the same render")
}
