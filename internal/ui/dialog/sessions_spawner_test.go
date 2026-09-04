package dialog

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

func TestSpawnerLabels(t *testing.T) {
	t.Parallel()
	active := []session.Session{
		{ID: "orch", Title: "Orchestrator", Color: "aliceblue", Animal: "tiger"},
		{ID: "worker", Title: "Worker", SpawnedBySessionID: "orch"},
		{ID: "stray", Title: "Stray", SpawnedBySessionID: "elsewhere"},
		{ID: "self", Title: "Self", SpawnedBySessionID: "self"},
	}
	archived := []session.Session{
		{ID: "old-worker", Title: "Old", SpawnedBySessionID: "orch"},
		{ID: "no-identity-child", SpawnedBySessionID: "no-identity"},
		{ID: "no-identity"},
	}
	got := spawnerLabels(active, archived)
	require.Equal(t, map[string]string{
		"worker":     "by aliceblue-tiger",
		"old-worker": "by aliceblue-tiger",
	}, got, "only spawners present with an identity produce a note")
}

// The note lands in the info column next to the relative time so the
// row reads "Worker            by aliceblue-tiger · now".
func TestSessionItem_RenderShowsSpawner(t *testing.T) {
	t.Parallel()
	sty := themes.CharmtonePantera()
	items := sessionItems(&sty, sessionsModeNormal, map[string]string{"worker": "by aliceblue-tiger"},
		session.Session{ID: "worker", Title: "Worker"},
		session.Session{ID: "orch", Title: "Orchestrator"},
	)
	require.Len(t, items, 2)
	worker := items[0].(*SessionItem)
	orch := items[1].(*SessionItem)
	require.Equal(t, "by aliceblue-tiger", worker.spawnerLabel)
	require.Empty(t, orch.spawnerLabel)
	require.Contains(t, ansi.Strip(worker.Render(80)), "by aliceblue-tiger ·")
	require.NotContains(t, ansi.Strip(orch.Render(80)), "by ")

	var _ list.FilterableItem = worker
}
