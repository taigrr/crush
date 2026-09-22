package voice

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/oauth"
)

func grokConfig(tok *oauth.Token) *config.Config {
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	cfg.Providers.Set("grok", config.ProviderConfig{ID: "grok", BaseURL: "https://cli-chat-proxy.grok.com/v1", APIKey: tok.AccessToken, OAuthToken: tok})
	return cfg
}

type fakeRefresher struct {
	calls atomic.Int32
	cfg   *config.Config
	next  *oauth.Token
}

func (f *fakeRefresher) RefreshOAuthToken(_ context.Context, _ config.Scope, providerID string) error {
	f.calls.Add(1)
	pc, _ := f.cfg.Providers.Get(providerID)
	pc.OAuthToken, pc.APIKey = f.next, f.next.AccessToken
	f.cfg.Providers.Set(providerID, pc)
	return nil
}

func TestBearerFuncRefreshesExpiredOAuthToken(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	cfg := grokConfig(&oauth.Token{AccessToken: "old", ExpiresAt: time.Now().Add(-time.Hour).Unix(), ExpiresIn: 3600})
	ref := &fakeRefresher{cfg: cfg, next: &oauth.Token{AccessToken: "new", ExpiresAt: time.Now().Add(time.Hour).Unix(), ExpiresIn: 3600}}
	bearerFn := NewBearerFunc(func() *config.Config { return cfg }, ref, Config{})

	bearer, err := bearerFn(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "new", bearer)

	bearer, err = bearerFn(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "new", bearer)
	require.Equal(t, int32(1), ref.calls.Load(), "valid token must not refresh again")

	_, err = bearerFn(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, int32(2), ref.calls.Load(), "force refresh always refreshes")
}

var insecureTLS = &tls.Config{InsecureSkipVerify: true}

func sttTestServer(t *testing.T, rejectStatus, rejectUntil int) (wsBase string, bearers *csync.Slice[string]) {
	t.Helper()
	bearers = csync.NewSlice[string]()
	var attempts atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearers.Append(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if int(attempts.Add(1)) <= rejectUntil {
			w.WriteHeader(rejectStatus)
			_, _ = w.Write([]byte(`{"error":"token rejected"}`))
			return
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"transcript.created"}`))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "wss" + strings.TrimPrefix(srv.URL, "https"), bearers
}

func TestConnectWithAuthRetriesOnceOn403(t *testing.T) {
	t.Parallel()
	wsBase, bearers := sttTestServer(t, http.StatusForbidden, 1)
	bearerFn := func(_ context.Context, force bool) (string, error) {
		if force {
			return "fresh", nil
		}
		return "stale", nil
	}
	stt, err := connectWithAuth(context.Background(), Config{APIBase: wsBase}.Normalize(), bearerFn, insecureTLS)
	require.NoError(t, err)
	stt.close()
	require.Equal(t, []string{"stale", "fresh"}, bearers.Copy())

	wsBase, bearers = sttTestServer(t, http.StatusForbidden, 99)
	_, err = connectWithAuth(context.Background(), Config{APIBase: wsBase}.Normalize(), bearerFn, insecureTLS)
	var ve *Error
	require.ErrorAs(t, err, &ve)
	require.Equal(t, http.StatusForbidden, ve.HTTPStatus)
	require.Contains(t, ve.Msg, "token rejected")
	require.Len(t, bearers.Copy(), 2, "exactly one retry")
}
