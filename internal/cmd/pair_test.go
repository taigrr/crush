package cmd

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/pairing"
)

func TestResolveDevice(t *testing.T) {
	t.Parallel()
	devices := []pairing.Device{
		{DeviceID: "dev_aaaa1", Name: "Tai's iPhone"},
		{DeviceID: "dev_aaab2", Name: "Pixel"},
	}
	d, err := resolveDevice(devices, "dev_aaab2")
	require.NoError(t, err)
	require.Equal(t, "Pixel", d.Name)
	d, err = resolveDevice(devices, "tai's iphone")
	require.NoError(t, err)
	require.Equal(t, "dev_aaaa1", d.DeviceID)
	_, err = resolveDevice(devices, "dev_aaa")
	require.ErrorContains(t, err, "matches 2")
	_, err = resolveDevice(devices, "nope")
	require.ErrorContains(t, err, "no paired device")
}

func TestConfirmSAS(t *testing.T) {
	t.Parallel()
	view := pairing.OfferView{
		Device: &pairing.DeviceMeta{DeviceID: "dev_x", Name: "Phone", Platform: "ios"},
		SAS:    &pairing.SASView{Emoji: []string{"🐶", "🐱", "🦁", "🐎"}, Names: []string{"Dog", "Cat", "Lion", "Horse"}, Digits: "123 456"},
	}
	for input, want := range map[string]bool{"y\n": true, "YES\n": true, "n\n": false, "\n": false, "": false} {
		cmd := &cobra.Command{}
		cmd.Flags().Bool(flagYes, false, "")
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetIn(strings.NewReader(input))
		got, err := confirmSAS(cmd, view)
		require.NoError(t, err)
		require.Equal(t, want, got, "input %q", input)
		require.Contains(t, out.String(), "123 456")
		require.Contains(t, out.String(), "Dog, Cat, Lion, Horse")
	}
}

// fakePairServer serves the local pairing API from a real Manager.
func fakePairServer(t *testing.T, mgr *pairing.Manager) *client.Client {
	t.Helper()
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any, err error) {
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	}
	id := func(r *http.Request) []byte {
		b, err := pairing.ParseOfferID(r.PathValue("id"))
		require.NoError(t, err)
		return b
	}
	mux.HandleFunc("POST /v1/cluster/pairing/offers", func(w http.ResponseWriter, _ *http.Request) {
		v, err := mgr.CreateNodeOffer(0, pairing.RoleOperator)
		reply(w, v, err)
	})
	mux.HandleFunc("GET /v1/cluster/pairing/offers/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := mgr.Wait(r.Context(), id(r), pairing.State(r.URL.Query().Get("state")), 2*time.Second)
		reply(w, v, err)
	})
	mux.HandleFunc("POST /v1/cluster/pairing/offers/{id}/approve", func(w http.ResponseWriter, r *http.Request) {
		v, err := mgr.Approve(id(r), "")
		reply(w, v, err)
	})
	mux.HandleFunc("POST /v1/cluster/pairing/offers/{id}/reject", func(w http.ResponseWriter, r *http.Request) {
		v, err := mgr.Reject(id(r))
		reply(w, v, err)
	})
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	c, err := client.NewClient("", "tcp", hs.Listener.Addr().String())
	require.NoError(t, err)
	return c
}

func TestRunPairingEndToEnd(t *testing.T) {
	t.Parallel()
	_, nodeKey, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	self := pairing.Self{ClusterID: "c", NodeID: "n", NodeName: "node", NodeKey: nodeKey, CAFP: pairing.RandomBytes(pairing.FingerprintSize)}

	for _, tc := range []struct {
		name    string
		answer  string
		wantErr string
	}{
		{"match", "y\n", ""},
		{"mismatch", "n\n", "did not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mgr := pairing.NewManager(pairing.Config{Self: func() (pairing.Self, error) { return self, nil }})
			c := fakePairServer(t, mgr)
			offer, err := c.PairCreateOffer(t.Context(), pairing.CreateOfferRequest{})
			require.NoError(t, err)

			// The phone claims and confirms in the background.
			go func() {
				p, _ := pairing.DecodePayload(offer.Payload)
				_, key, _ := ed25519.GenerateKey(nil)
				sess, _ := pairing.NewDeviceSessionFromPayload(key, p)
				resp, err := mgr.Claim(sess.Claim(self.Info(), "Phone", "ios"))
				if err != nil {
					return
				}
				if _, err := sess.Accept(resp); err == nil {
					_, _ = mgr.Confirm(sess.Confirm())
				}
			}()

			cmd := &cobra.Command{}
			cmd.Flags().Bool(flagYes, false, "")
			cmd.SetContext(t.Context())
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetIn(strings.NewReader(tc.answer))
			err = runPairing(cmd, c, offer)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err, out.String())
			require.Contains(t, out.String(), "Paired Phone")
		})
	}
}
