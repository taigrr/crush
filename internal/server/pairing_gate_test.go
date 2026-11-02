package server

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/pairing"
)

const gateWait = 10 * time.Second

func dialable(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func requireBound(t *testing.T, srv *Server, addr string) {
	t.Helper()
	require.Eventually(t, func() bool { return srv.pairing.client.Running() && dialable(addr) }, gateWait, 20*time.Millisecond, "listener should be bound")
}

func requireUnbound(t *testing.T, srv *Server, addr string) {
	t.Helper()
	require.Eventually(t, func() bool { return !srv.pairing.client.Running() && !dialable(addr) }, gateWait, 20*time.Millisecond, "listener should be unbound")
}

func requireStaysUnbound(t *testing.T, srv *Server, addr string) {
	t.Helper()
	require.Never(t, func() bool { return srv.pairing.client.Running() || dialable(addr) }, 500*time.Millisecond, 20*time.Millisecond, "listener must not be bound")
}

func TestGateBootWithoutDevicesIsUnbound(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, _ := initSingleNodeWith(t, addr)
	requireStaysUnbound(t, srv, addr)
}

func TestGateOfferOpensAndExpiryCloses(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, c := initSingleNodeWith(t, addr)
	_, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{TTLSeconds: 1})
	require.NoError(t, err)
	requireBound(t, srv, addr)
	// No event fires on expiry; the gate wakes for it.
	requireUnbound(t, srv, addr)
}

func TestGateRejectedOfferCloses(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, c := initSingleNodeWith(t, addr)
	offer, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	requireBound(t, srv, addr)
	_, err = c.PairReject(t.Context(), offer.OfferID)
	require.NoError(t, err)
	requireUnbound(t, srv, addr)
}

func TestGatePairThenRevokeLocally(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, c := initSingleNodeWith(t, addr)
	poll := pairFlowA(t, srv, c, newDeviceKey(t))
	// The offer is finished, but the device keeps the listener up.
	requireBound(t, srv, addr)
	require.Never(t, func() bool { return !srv.pairing.client.Running() }, 300*time.Millisecond, 20*time.Millisecond)

	_, err := c.RevokeDevice(t.Context(), poll.Device.DeviceID)
	require.NoError(t, err)
	requireUnbound(t, srv, addr)
}

func TestGateBootWithDeviceIsBound(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, c := initSingleNodeWith(t, addr)
	pairFlowA(t, srv, c, newDeviceKey(t))
	requireBound(t, srv, addr)

	// Restart the mesh (as on boot): the active device binds the port.
	srv.stopMesh()
	requireUnbound(t, srv, addr)
	require.NoError(t, srv.startMesh())
	requireBound(t, srv, addr)
}

func TestGateRevokeViaReplicationCloses(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, c := initSingleNodeWith(t, addr)
	poll := pairFlowA(t, srv, c, newDeviceKey(t))
	requireBound(t, srv, addr)

	// A peer's revocation arrives over the mesh.
	d := *poll.Device
	now := time.Now().UTC()
	d.RevokedAt, d.Rev, d.UpdatedAt, d.UpdatedBy = &now, d.Rev+1, now, "peer"
	data, err := json.Marshal(d)
	require.NoError(t, err)
	srv.pairing.receive(&nats.Msg{Subject: subjDevices, Data: data})
	requireUnbound(t, srv, addr)
}

func TestGatePortBusyRetries(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, c := initSingleNodeWith(t, addr)
	busy, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	_, err = c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	require.Never(t, srv.pairing.client.Running, 300*time.Millisecond, 20*time.Millisecond)

	require.NoError(t, busy.Close())
	srv.pairing.kickGate()
	requireBound(t, srv, addr)
}

func TestGateConcurrentOffersAndRevokes(t *testing.T) {
	t.Parallel()
	addr := freeTCPAddr(t)
	srv, c := initSingleNodeWith(t, addr)
	p := srv.pairing

	var devices []pairing.Device
	for range 4 {
		devices = append(devices, *pairFlowA(t, srv, c, newDeviceKey(t)).Device)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 10 {
				view, err := p.mgr.CreateNodeOffer(0, pairing.RoleOperator)
				if err != nil {
					continue
				}
				id, _ := pairing.ParseOfferID(view.OfferID)
				_, _ = p.mgr.Reject(id)
			}
		})
		d := devices[i%len(devices)]
		wg.Go(func() { _, _ = p.reg.Revoke(d.DeviceID, "n", time.Now()) })
		wg.Go(func() { p.kickGate(); _ = p.client.Running() })
	}
	wg.Wait()
	// Nothing left to serve: no open offer and every device revoked.
	n, _ := p.mgr.Active()
	require.Zero(t, n)
	require.False(t, p.reg.HasActive())
	requireUnbound(t, srv, addr)

	// And it comes back for a new offer.
	_, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	requireBound(t, srv, addr)
}
