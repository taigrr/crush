package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/backend"
	"github.com/taigrr/crush/internal/cluster"
)

func TestCredsSyncAdoptsOnlyNewerValidContent(t *testing.T) {
	t.Parallel()
	node, err := cluster.NewNode(t.TempDir(), "v1")
	require.NoError(t, err)
	br := &meshBridge{node: node, backend: backend.New(t.Context(), nil, nil)}
	path := filepath.Join(t.TempDir(), "crush.json")
	c := newCredsSync(br, path)

	deliver := func(content string, mod time.Time) {
		data, _ := json.Marshal(credsMsg{NodeID: "peer", ModTime: mod.UnixNano(), Sum: sum([]byte(content)), Content: []byte(content)})
		c.receive(&nats.Msg{Data: data})
	}

	// Missing locally: adopt.
	t0 := time.Now().Add(-time.Hour)
	deliver(`{"providers":{"a":{"api_key":"k1"}}}`, t0)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(got), "k1")
	fi, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	// Older: ignored.
	deliver(`{"providers":{"a":{"api_key":"old"}}}`, t0.Add(-time.Minute))
	got, _ = os.ReadFile(path)
	require.Contains(t, string(got), "k1")

	// Not JSON: ignored even if newer.
	deliver(`not json`, time.Now())
	got, _ = os.ReadFile(path)
	require.Contains(t, string(got), "k1")

	// Newer: adopted.
	deliver(`{"providers":{"a":{"api_key":"k2"}}}`, time.Now())
	got, _ = os.ReadFile(path)
	require.Contains(t, string(got), "k2")
}
