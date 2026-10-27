package cluster

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
)

// pairedTrio builds three approved members, each with an advertised
// mesh endpoint, and returns them with their mesh addresses.
func pairedTrio(t *testing.T) ([]*Node, []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	nodes := []*Node{newTestNode(t, "v1.0.0"), newTestNode(t, "v1.0.0"), newTestNode(t, "v1.0.0")}
	meshAddrs := []string{freeAddr(t), freeAddr(t), freeAddr(t)}

	apiA := freeAddr(t)
	token, err := nodes[0].Init([]Endpoint{
		{Kind: EndpointDirect, Addr: apiA},
		{Kind: EndpointNATS, Addr: meshAddrs[0]},
	})
	require.NoError(t, err)
	require.NoError(t, nodes[0].Start(apiA))

	for i := 1; i < 3; i++ {
		joiner := nodes[i]
		api := freeAddr(t)
		done := make(chan error, 1)
		go func() {
			done <- joiner.Join(ctx, nodes[0].Listening(), token, JoinOptions{
				Endpoints: []Endpoint{
					{Kind: EndpointDirect, Addr: api},
					{Kind: EndpointNATS, Addr: meshAddrs[i]},
				},
				PollInterval: 20 * time.Millisecond,
				OnPending: func(string) {
					go func() { _, _ = nodes[0].Approve(ctx, joiner.Identity().ID) }()
				},
			})
		}()
		require.NoError(t, <-done)
		require.NoError(t, joiner.Start(api))
	}
	return nodes, meshAddrs
}

func TestMeshDeliversAcrossNodes(t *testing.T) {
	t.Parallel()
	nodes, addrs := pairedTrio(t)
	var meshes []*Mesh
	for i, n := range nodes {
		m, err := n.StartMesh(addrs[i])
		require.NoError(t, err)
		t.Cleanup(m.Close)
		meshes = append(meshes, m)
	}
	require.Eventually(t, func() bool {
		for _, m := range meshes {
			if m.NumRoutes() < 2 {
				return false
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond)

	got := make(chan string, 1)
	_, err := meshes[2].Subscribe("crush.test", func(msg *nats.Msg) { got <- string(msg.Data) })
	require.NoError(t, err)
	require.NoError(t, meshes[2].Conn().Flush())

	_, err = meshes[1].Subscribe("crush.echo", func(msg *nats.Msg) { _ = msg.Respond(append([]byte("echo:"), msg.Data...)) })
	require.NoError(t, err)
	require.NoError(t, meshes[1].Conn().Flush())

	require.Eventually(t, func() bool {
		_ = meshes[0].Conn().Publish("crush.test", []byte("hello"))
		select {
		case v := <-got:
			return v == "hello"
		case <-time.After(100 * time.Millisecond):
			return false
		}
	}, 10*time.Second, 50*time.Millisecond)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	rsp, err := meshes[0].Request(ctx, "crush.echo", []byte("ping"))
	require.NoError(t, err)
	require.Equal(t, "echo:ping", string(rsp.Data))
}

func TestMeshRejectsNonMembers(t *testing.T) {
	t.Parallel()
	nodes, addrs := pairedTrio(t)
	m0, err := nodes[0].StartMesh(addrs[0])
	require.NoError(t, err)
	t.Cleanup(m0.Close)

	// A node holding the key but never approved presents a valid
	// certificate; the mesh must still refuse the TLS handshake.
	st, err := nodes[0].Store().Snapshot()
	require.NoError(t, err)
	outsider := newTestNode(t, "v1.0.0")
	cert, err := IssueNodeCert(st.ClusterID, st.Key, outsider.Identity())
	require.NoError(t, err)
	_, clientTLS := tlsConfigs(
		func() (*tlsCertificate, error) { return &cert, nil },
		func() [][]byte { return [][]byte{st.Key} },
	)
	require.Eventually(t, func() bool {
		c, err := net.DialTimeout("tcp", addrs[0], time.Second)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}, 5*time.Second, 50*time.Millisecond)
	conn, err := tlsDial(addrs[0], clientTLS)
	if err == nil {
		// TLS 1.3 reports client-cert rejection on first read.
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, err = conn.Read(make([]byte, 1))
		_ = conn.Close()
	}
	require.Error(t, err)
}
