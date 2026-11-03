package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/pairing"
)

func TestPinnedAddr(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, port, want string
		bad            bool
	}{
		{in: "192.168.9.238", port: "7735", want: "192.168.9.238:7735"},
		{in: "192.168.9.238:9000", port: "7735", want: "192.168.9.238:9000"},
		{in: " box.lan ", port: "7735", want: "box.lan:7735"},
		{in: "fd00::1", port: "7735", want: "[fd00::1]:7735"},
		{in: "[fd00::1]", port: "7735", want: "[fd00::1]:7735"},
		{in: "[fd00::1]:9", port: "7735", want: "[fd00::1]:9"},
		{in: "box.lan", port: "", bad: true},
		{in: "http://box.lan", port: "1", bad: true},
		{in: "box.lan:notaport", port: "1", bad: true},
		{in: "box.lan:99999", port: "1", bad: true},
		{in: "", port: "1", bad: true},
	}
	for _, tt := range tests {
		got, err := pinnedAddr(tt.in, tt.port)
		if tt.bad {
			require.Error(t, err, tt.in)
			continue
		}
		require.NoError(t, err, tt.in)
		require.Equal(t, tt.want, got)
	}
}

// TestPairOfferPinnedHosts checks that --host replaces the advertised
// endpoints in the QR payload, in order, keeping the listener port.
func TestPairOfferPinnedHosts(t *testing.T) {
	isolateHome(t)
	srv, c := initSingleNodeWith(t, freeTCPAddr(t))
	view, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{Hosts: []string{"192.168.9.238", "box.lan:9999"}})
	require.NoError(t, err)
	p, err := pairing.DecodePayload(view.Payload)
	require.NoError(t, err)
	port := portOfAny([]string{srv.pairing.clientAddr})
	require.NotEmpty(t, port)
	require.Equal(t, []pairing.Endpoint{
		{Kind: pairing.EndpointLAN, Addr: "192.168.9.238:" + port},
		{Kind: pairing.EndpointDirect, Addr: "box.lan:9999"},
	}, p.Endpoints)

	_, err = c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{Hosts: []string{"127.0.0.1"}})
	require.Error(t, err)
	_, err = c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{Hosts: []string{"http://x"}})
	require.Error(t, err)
}
