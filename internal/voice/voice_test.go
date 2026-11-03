package voice

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMint(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	tests := []struct {
		name    string
		status  int
		body    string
		want    Secret
		wantErr string
	}{
		{"flat", 200, `{"value":"eph","expires_at":1000300}`, Secret{Value: "eph", ExpiresAt: time.Unix(1000300, 0)}, ""},
		{"nested", 200, `{"client_secret":{"value":"eph2","expires_at":1000100}}`, Secret{Value: "eph2", ExpiresAt: time.Unix(1000100, 0)}, ""},
		{"no expiry uses ttl", 200, `{"value":"eph3"}`, Secret{Value: "eph3", ExpiresAt: now.Add(DefaultTTL)}, ""},
		{"no value", 200, `{}`, Secret{}, "no value"},
		{"upstream error not echoed", 401, `{"error":"bad key fake-key"}`, Secret{}, "HTTP 401"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				if r.Header.Get("Authorization") != "Bearer fake-key" || r.Header.Get("X-Extra") != "1" || !strings.Contains(string(raw), `"seconds":300`) {
					http.Error(w, "bad", http.StatusBadRequest)
					return
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			m := Minter{BaseURL: srv.URL + "/v1/", Client: srv.Client(), Now: func() time.Time { return now }}
			got, err := m.Mint(t.Context(), Credential{Token: "fake-key", Headers: map[string]string{"X-Extra": "1"}})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				require.NotContains(t, err.Error(), "fake-key")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want.Value, got.Value)
			require.True(t, tt.want.ExpiresAt.Equal(got.ExpiresAt))
		})
	}
}

func TestMintNoCredential(t *testing.T) {
	t.Parallel()
	_, err := (&Minter{BaseURL: "http://127.0.0.1:1"}).Mint(t.Context(), Credential{})
	require.True(t, errors.Is(err, ErrNoCredential))
}
