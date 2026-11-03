package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/pairing"
	"github.com/taigrr/crush/internal/proto"
)

// deviceAPI talks to a node's /client/v1 endpoints like rush would.
type deviceAPI struct {
	t    *testing.T
	base string
	hc   *http.Client
}

func newDeviceAPI(t *testing.T, srv *Server) *deviceAPI {
	t.Helper()
	hs := httptest.NewServer(srv.pairing.clientHandler())
	t.Cleanup(hs.Close)
	return &deviceAPI{t: t, base: hs.URL, hc: hs.Client()}
}

func (d *deviceAPI) do(method, path string, in, out any) (int, pairing.ErrorCode) {
	d.t.Helper()
	var body bytes.Buffer
	if in != nil {
		require.NoError(d.t, json.NewEncoder(&body).Encode(in))
	}
	req, err := http.NewRequestWithContext(d.t.Context(), method, d.base+path, &body)
	require.NoError(d.t, err)
	rsp, err := d.hc.Do(req)
	require.NoError(d.t, err)
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		var pe pairing.Error
		_ = json.NewDecoder(rsp.Body).Decode(&pe)
		return rsp.StatusCode, pe.Code
	}
	if out != nil {
		require.NoError(d.t, json.NewDecoder(rsp.Body).Decode(out))
	}
	return rsp.StatusCode, ""
}

func (d *deviceAPI) info() pairing.Info {
	var info pairing.Info
	status, _ := d.do(http.MethodGet, "/client/v1/info", nil, &info)
	require.Equal(d.t, http.StatusOK, status)
	return info
}

func initSingleNode(t *testing.T) (*Server, *client.Client) {
	t.Helper()
	return initSingleNodeWith(t, "")
}

func initSingleNodeWith(t *testing.T, clientAddr string) (*Server, *client.Client) {
	t.Helper()
	srv, c := newClusterTestServerWith(t, clientAddr)
	_, err := c.ClusterInit(t.Context(), proto.ClusterInitRequest{
		Endpoints: []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: srv.clusterAddr}},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return srv.mesh != nil }, 10*time.Second, 20*time.Millisecond)
	return srv, c
}

func newDeviceKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	return key
}

// pairFlowA runs flow (a) to completion and returns the device record.
func pairFlowA(t *testing.T, srv *Server, c *client.Client, key ed25519.PrivateKey) pairing.PollResponse {
	t.Helper()
	ctx := t.Context()
	dev := newDeviceAPI(t, srv)
	offer, err := c.PairCreateOffer(ctx, pairing.CreateOfferRequest{})
	require.NoError(t, err)
	require.Equal(t, pairing.StateOpen, offer.State)
	payload, err := pairing.DecodePayload(offer.Payload)
	require.NoError(t, err)
	info := dev.info()
	require.NoError(t, pairing.CheckInfo(payload, info))

	sess, err := pairing.NewDeviceSessionFromPayload(key, payload)
	require.NoError(t, err)
	var claim pairing.ClaimResponse
	status, code := dev.do(http.MethodPost, "/client/v1/pair/claim", sess.Claim(info, "Test Phone", "ios"), &claim)
	require.Equal(t, http.StatusOK, status, code)
	sas, err := sess.Accept(claim)
	require.NoError(t, err)

	// The operator side sees the same SAS.
	view, err := c.PairWaitOffer(ctx, offer.OfferID, pairing.StateOpen, 5000)
	require.NoError(t, err)
	require.Equal(t, pairing.StateClaimed, view.State)
	require.Equal(t, sas.DigitString(), view.SAS.Digits)
	require.Equal(t, "Test Phone", view.Device.Name)

	status, _ = dev.do(http.MethodPost, "/client/v1/pair/confirm", sess.Confirm(), nil)
	require.Equal(t, http.StatusOK, status)
	_, err = c.PairApprove(ctx, offer.OfferID, "")
	require.NoError(t, err)

	var poll pairing.PollResponse
	status, _ = dev.do(http.MethodPost, "/client/v1/pair/poll", sess.Poll(1000), &poll)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, pairing.StateApproved, poll.State)
	return poll
}

// deviceConn connects like rush: wss to the node's device websocket,
// pinned to the cluster CA and node, authenticated by signing the
// server nonce with the device key, with a private inbox prefix.
func deviceConn(t *testing.T, srv *Server, key ed25519.PrivateKey, opts ...nats.Option) (*nats.Conn, error) {
	t.Helper()
	self, err := srv.pairing.self()
	require.NoError(t, err)
	kp, err := nkeys.FromRawSeed(nkeys.PrefixByteUser, key.Seed())
	require.NoError(t, err)
	user, err := kp.PublicKey()
	require.NoError(t, err)
	id := pairing.DeviceID(key.Public().(ed25519.PublicKey))
	cas, err := srv.Cluster().CACertificates()
	require.NoError(t, err)
	tlsCfg, err := pairing.TrustedTLSConfig(cas, [][]byte{self.CAFP}, self.NodeID)
	require.NoError(t, err)
	base := []nats.Option{
		nats.Secure(tlsCfg),
		nats.Nkey(user, kp.Sign), nats.NoReconnect(), nats.Timeout(3 * time.Second),
		nats.CustomInboxPrefix("_INBOX." + id),
	}
	return nats.Connect("wss://"+srv.pairing.wsAddr, append(base, opts...)...)
}

func TestPairingFlowAHappyPathAndRevoke(t *testing.T) {
	t.Parallel()
	srv, c := initSingleNode(t)
	key := newDeviceKey(t)

	// Unknown devices cannot connect to NATS.
	_, err := deviceConn(t, srv, key)
	require.Error(t, err)

	poll := pairFlowA(t, srv, c, key)
	require.Equal(t, pairing.DeviceID(key.Public().(ed25519.PublicKey)), poll.Device.DeviceID)
	require.Equal(t, pairing.DefaultRole, poll.Device.Role)
	require.ElementsMatch(t, pairing.AllScopes(), poll.Device.Scopes)
	require.Equal(t, pairing.ClientsAccount, poll.NATS.Account)
	require.Equal(t, pairing.DevicePermissions(poll.Device.DeviceID), poll.NATS.Permissions)
	require.Len(t, poll.CAFPs, 1)
	require.Len(t, poll.Members, 1)

	devices, err := c.Devices(t.Context())
	require.NoError(t, err)
	require.Len(t, devices, 1)

	// The scoped NATS user is installed.
	nc, err := deviceConn(t, srv, key)
	require.NoError(t, err)
	closed := make(chan struct{})
	nc.SetClosedHandler(func(*nats.Conn) { close(closed) })

	revoked, err := c.RevokeDevice(t.Context(), poll.Device.DeviceID)
	require.NoError(t, err)
	require.True(t, revoked.Revoked())
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked device stayed connected")
	}
	_, err = deviceConn(t, srv, key)
	require.Error(t, err)

	_, err = c.RevokeDevice(t.Context(), "dev_unknown")
	require.Error(t, err)

	// A revoked key cannot pair again (its record would lose to the
	// revocation and the phone would look paired but never connect).
	dev := newDeviceAPI(t, srv)
	offer, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	payload, _ := pairing.DecodePayload(offer.Payload)
	sess, _ := pairing.NewDeviceSessionFromPayload(key, payload)
	status, code := dev.do(http.MethodPost, "/client/v1/pair/claim", sess.Claim(dev.info(), "again", "ios"), nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, pairing.ErrCodeBadRequest, code)
}

func TestPairingExpiredOffer(t *testing.T) {
	t.Parallel()
	srv, c := initSingleNode(t)
	dev := newDeviceAPI(t, srv)
	offer, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{TTLSeconds: 1})
	require.NoError(t, err)
	payload, err := pairing.DecodePayload(offer.Payload)
	require.NoError(t, err)
	time.Sleep(1100 * time.Millisecond)
	sess, err := pairing.NewDeviceSessionFromPayload(newDeviceKey(t), payload)
	require.NoError(t, err)
	status, code := dev.do(http.MethodPost, "/client/v1/pair/claim", sess.Claim(dev.info(), "x", "ios"), nil)
	require.Equal(t, http.StatusGone, status)
	require.Equal(t, pairing.ErrCodeOfferExpired, code)
}

func TestPairingReusedSecret(t *testing.T) {
	t.Parallel()
	srv, c := initSingleNode(t)
	key := newDeviceKey(t)
	dev := newDeviceAPI(t, srv)
	offer, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	payload, _ := pairing.DecodePayload(offer.Payload)
	info := dev.info()
	first, _ := pairing.NewDeviceSessionFromPayload(key, payload)
	status, _ := dev.do(http.MethodPost, "/client/v1/pair/claim", first.Claim(info, "x", "ios"), nil)
	require.Equal(t, http.StatusOK, status)

	// Replaying the leaked QR is rejected and burns the offer.
	second, _ := pairing.NewDeviceSessionFromPayload(newDeviceKey(t), payload)
	status, code := dev.do(http.MethodPost, "/client/v1/pair/claim", second.Claim(info, "y", "ios"), nil)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, pairing.ErrCodeOfferUsed, code)
	view, err := c.PairWaitOffer(t.Context(), offer.OfferID, "", 0)
	require.NoError(t, err)
	require.Equal(t, pairing.StateAborted, view.State)
	_, err = c.PairApprove(t.Context(), offer.OfferID, "")
	require.Error(t, err)

	// Bad bodies are rejected cleanly.
	status, code = dev.do(http.MethodPost, "/client/v1/pair/claim", map[string]any{"offer_id": "!!"}, nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, pairing.ErrCodeBadRequest, code)
}

func TestPairingMismatchAbort(t *testing.T) {
	t.Parallel()
	srv, c := initSingleNode(t)
	dev := newDeviceAPI(t, srv)
	offer, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	payload, _ := pairing.DecodePayload(offer.Payload)
	sess, _ := pairing.NewDeviceSessionFromPayload(newDeviceKey(t), payload)
	var claim pairing.ClaimResponse
	status, _ := dev.do(http.MethodPost, "/client/v1/pair/claim", sess.Claim(dev.info(), "x", "ios"), &claim)
	require.Equal(t, http.StatusOK, status)
	_, err = sess.Accept(claim)
	require.NoError(t, err)

	var st pairing.StateResponse
	status, _ = dev.do(http.MethodPost, "/client/v1/pair/abort", sess.Abort(pairing.ReasonSASMismatch), &st)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, pairing.StateAborted, st.State)
	view, err := c.PairWaitOffer(t.Context(), offer.OfferID, "", 0)
	require.NoError(t, err)
	require.Equal(t, pairing.ReasonSASMismatch, view.Reason)

	status, code := dev.do(http.MethodPost, "/client/v1/pair/poll", sess.Poll(0), nil)
	require.Equal(t, http.StatusGone, status)
	require.Equal(t, pairing.ErrCodeOfferAborted, code)
	devices, err := c.Devices(t.Context())
	require.NoError(t, err)
	require.Empty(t, devices)

	// The operator can reject from the node side too.
	offer2, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
	require.NoError(t, err)
	rejected, err := c.PairReject(t.Context(), offer2.OfferID)
	require.NoError(t, err)
	require.Equal(t, pairing.StateRejected, rejected.State)
}

func TestPairingFlowBTypedCodeOverTLS(t *testing.T) {
	t.Parallel()
	srv, c := initSingleNodeWith(t, freeTCPAddr(t))
	key := newDeviceKey(t)
	secret := pairing.RandomBytes(pairing.DeviceSecretSize)
	code, err := pairing.EncodeCode(secret)
	require.NoError(t, err)
	offer, err := c.PairAccept(t.Context(), pairing.AcceptRequest{Code: code})
	require.NoError(t, err)
	info, err := pairing.DecodePayload(offer.InfoPayload)
	require.NoError(t, err)
	require.Equal(t, pairing.KindNodeInfo, info.Kind)

	// The phone pins the CA fingerprint from the kind 3 code and the node
	// ID from the leaf.
	var leafCN string
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, // Verified by VerifyConnection below.
		VerifyConnection: func(cs tls.ConnectionState) error {
			leafCN = cs.PeerCertificates[0].Subject.CommonName
			_, err := x509.ParseCertificate(cs.PeerCertificates[0].Raw)
			return err
		},
	}}}
	require.Eventually(t, func() bool {
		rsp, err := hc.Get("https://" + srv.clientAddr + "/client/v1/info")
		if err == nil {
			rsp.Body.Close()
		}
		return err == nil
	}, 10*time.Second, 50*time.Millisecond)
	dev := &deviceAPI{t: t, base: "https://" + srv.clientAddr, hc: hc}
	nodeInfo := dev.info()
	require.NoError(t, pairing.CheckInfo(info, nodeInfo))
	require.Equal(t, nodeInfo.NodeID, leafCN)

	sess := pairing.NewDeviceSessionFromSecret(key, secret)
	var claim pairing.ClaimResponse
	status, _ := dev.do(http.MethodPost, "/client/v1/pair/claim", sess.Claim(nodeInfo, "Pixel", "android"), &claim)
	require.Equal(t, http.StatusOK, status)
	_, err = sess.Accept(claim)
	require.NoError(t, err)
	_, err = c.PairApprove(t.Context(), offer.OfferID, string(pairing.RoleViewer))
	require.NoError(t, err)
	status, _ = dev.do(http.MethodPost, "/client/v1/pair/confirm", sess.Confirm(), nil)
	require.Equal(t, http.StatusOK, status)
	var poll pairing.PollResponse
	status, _ = dev.do(http.MethodPost, "/client/v1/pair/poll", sess.Poll(0), &poll)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, pairing.StateApproved, poll.State)
	require.Equal(t, pairing.DefaultRole, poll.Device.Role, "a requested role is ignored")
	require.ElementsMatch(t, pairing.AllScopes(), poll.Device.Scopes)
}

func TestPairingPropagatesAcrossCluster(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	a, ac := newClusterTestServer(t)
	b, bc := newClusterTestServer(t)
	token, err := ac.ClusterInit(ctx, proto.ClusterInitRequest{
		Endpoints: []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: a.clusterAddr}},
	})
	require.NoError(t, err)
	go func() {
		for ctx.Err() == nil {
			if st, err := ac.ClusterStatus(ctx); err == nil && len(st.Pending) == 1 {
				_, _ = ac.ClusterApprove(ctx, st.Pending[0].ID)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}()
	_, err = bc.ClusterJoin(ctx, proto.ClusterJoinRequest{
		Addr: a.clusterAddr, Token: token,
		Endpoints:      []cluster.Endpoint{{Kind: cluster.EndpointDirect, Addr: b.clusterAddr}},
		TimeoutSeconds: 20,
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return a.mesh != nil && b.mesh != nil && a.mesh.NumRoutes() > 0 && b.mesh.NumRoutes() > 0
	}, 15*time.Second, 50*time.Millisecond)

	key := newDeviceKey(t)
	poll := pairFlowA(t, a, ac, key)
	id := poll.Device.DeviceID

	// B learns the device and lets it connect.
	require.Eventually(t, func() bool {
		ds, err := bc.Devices(ctx)
		return err == nil && len(ds) == 1 && ds[0].DeviceID == id
	}, 15*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		nc, err := deviceConn(t, b, key)
		if err != nil {
			return false
		}
		nc.Close()
		return true
	}, 10*time.Second, 50*time.Millisecond)

	// A bridged request from a device on A runs on B, forwarded over the
	// mesh.
	dev := newFakeDevice(t, id, func(o ...nats.Option) (*nats.Conn, error) { return deviceConn(t, a, key, o...) })
	resp := dev.call(b.Cluster().Identity().ID, bridgeRequest{Method: "GET", Path: "/v1/workspace-overviews"})
	require.Equal(t, http.StatusOK, resp.Status)
	require.Equal(t, b.Cluster().Identity().ID, resp.NodeID)
	dev.nc.Close()

	// Revoking on B reaches A.
	_, err = bc.RevokeDevice(ctx, id)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		ds, err := ac.Devices(ctx)
		return err == nil && len(ds) == 1 && ds[0].Revoked()
	}, 15*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool {
		_, err := deviceConn(t, a, key)
		return err != nil
	}, 10*time.Second, 50*time.Millisecond)
}

func TestPairingNotInClusterAndEndpoints(t *testing.T) {
	t.Parallel()
	srv, _ := newClusterTestServer(t)
	dev := newDeviceAPI(t, srv)
	status, code := dev.do(http.MethodGet, "/client/v1/info", nil, nil)
	require.Equal(t, http.StatusConflict, status)
	require.Equal(t, pairing.ErrCodeNotInCluster, code)

	eps := clientEndpoints([]cluster.Endpoint{
		{Kind: cluster.EndpointDirect, Addr: ":7731"},
		{Kind: cluster.EndpointDirect, Addr: "127.0.0.1:7731"},
		{Kind: cluster.EndpointDirect, Addr: "0.0.0.0:7731"},
		{Kind: cluster.EndpointDirect, Addr: "192.168.1.5:7731"},
		{Kind: cluster.EndpointDirect, Addr: "100.100.1.2:7731"},
		{Kind: cluster.EndpointDirect, Addr: "laptop.example:7731"},
		{Kind: cluster.EndpointNATS, Addr: "192.168.1.5:7732"},
	}, DefaultClientPort)
	require.Equal(t, []pairing.Endpoint{
		{Kind: pairing.EndpointLAN, Addr: "192.168.1.5:7733"},
		{Kind: pairing.EndpointTailscale, Addr: "100.100.1.2:7733"},
		{Kind: pairing.EndpointDirect, Addr: "laptop.example:7733"},
	}, eps)
}

func TestPairingIgnoresUnknownFieldsAndBoundsBodies(t *testing.T) {
	t.Parallel()
	srv, _ := initSingleNode(t)
	dev := newDeviceAPI(t, srv)
	// A newer client's extra field is ignored (the request fails only on
	// its real content).
	status, code := dev.do(http.MethodPost, "/client/v1/pair/abort", map[string]any{"future": 1, "reason": "user_cancel", "offer_id": "AAAAAAAAAAAAAAAAAAAAAA", "mac": ""}, nil)
	require.Equal(t, http.StatusNotFound, status)
	require.Equal(t, pairing.ErrCodeOfferUnknown, code)

	huge := map[string]string{"device_name": strings.Repeat("x", maxPairBody)}
	status, code = dev.do(http.MethodPost, "/client/v1/pair/claim", huge, nil)
	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, pairing.ErrCodeBadRequest, code)
}
