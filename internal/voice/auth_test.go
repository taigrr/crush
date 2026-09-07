package voice

import (
	"context"
	"crypto/tls"
	"errors"
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

func grokConfig(tok *oauth.Token, apiKey string) *config.Config {
	cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	cfg.Providers.Set("grok", config.ProviderConfig{
		ID:         "grok",
		BaseURL:    "https://cli-chat-proxy.grok.com/v1",
		APIKey:     apiKey,
		OAuthToken: tok,
	})
	return cfg
}

type fakeRefresher struct {
	calls atomic.Int32
	cfg   *config.Config
	next  *oauth.Token
	err   error
}

func (f *fakeRefresher) RefreshOAuthToken(_ context.Context, scope config.Scope, providerID string) error {
	f.calls.Add(1)
	if f.err != nil {
		return f.err
	}
	pc, _ := f.cfg.Providers.Get(providerID)
	pc.OAuthToken = f.next
	pc.APIKey = f.next.AccessToken
	f.cfg.Providers.Set(providerID, pc)
	return nil
}

func TestResolveAPIBaseIgnoresGrokProxyBaseURL(t *testing.T) {
	t.Parallel()
	require.Equal(t, "https://api.x.ai", ResolveAPIBase(DefaultConfig()))
	require.Equal(t, "https://api.x.ai", ResolveAPIBase(Config{}))
	require.Equal(t, "https://stt.example", ResolveAPIBase(Config{APIBase: "https://stt.example/"}))
}

func TestBearerFuncRefreshesExpiredOAuthToken(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	expired := &oauth.Token{AccessToken: "old", ExpiresAt: time.Now().Add(-time.Hour).Unix(), ExpiresIn: 3600}
	cfg := grokConfig(expired, "old")
	fresh := &oauth.Token{AccessToken: "new", ExpiresAt: time.Now().Add(time.Hour).Unix(), ExpiresIn: 3600}
	ref := &fakeRefresher{cfg: cfg, next: fresh}

	bearer, err := NewBearerFunc(func() *config.Config { return cfg }, ref, Config{})(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "new", bearer)
	require.Equal(t, int32(1), ref.calls.Load())

	bearer, err = NewBearerFunc(func() *config.Config { return cfg }, ref, Config{})(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "new", bearer)
	require.Equal(t, int32(1), ref.calls.Load(), "valid token must not refresh again")
}

func TestBearerFuncForceRefresh(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	valid := &oauth.Token{AccessToken: "old", ExpiresAt: time.Now().Add(time.Hour).Unix(), ExpiresIn: 3600}
	cfg := grokConfig(valid, "old")
	fresh := &oauth.Token{AccessToken: "new", ExpiresAt: time.Now().Add(time.Hour).Unix(), ExpiresIn: 3600}
	ref := &fakeRefresher{cfg: cfg, next: fresh}

	bearer, err := NewBearerFunc(func() *config.Config { return cfg }, ref, Config{})(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, "new", bearer)

	failing := &fakeRefresher{cfg: cfg, err: errors.New("idp down")}
	_, err = NewBearerFunc(func() *config.Config { return cfg }, failing, Config{})(context.Background(), true)
	var ve *Error
	require.ErrorAs(t, err, &ve)
	require.Equal(t, ErrAuth, ve.Kind)
}

func TestBearerFuncPrefersDedicatedKeyAndEnv(t *testing.T) {
	t.Setenv("XAI_API_KEY", "env-key")
	cfg := grokConfig(nil, "provider-key")
	bearer, err := NewBearerFunc(func() *config.Config { return cfg }, nil, Config{APIKey: "dedicated"})(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "dedicated", bearer)
	bearer, err = NewBearerFunc(func() *config.Config { return cfg }, nil, Config{})(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "env-key", bearer)
}

func TestBearerFuncFallsBackToProviderAPIKey(t *testing.T) {
	t.Setenv("XAI_API_KEY", "")
	cfg := grokConfig(nil, "provider-key")
	bearer, err := NewBearerFunc(func() *config.Config { return cfg }, nil, Config{})(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, "provider-key", bearer)

	_, err = NewBearerFunc(func() *config.Config { return nil }, nil, Config{})(context.Background(), false)
	var ve *Error
	require.ErrorAs(t, err, &ve)
	require.Equal(t, ErrAuth, ve.Kind)
}

func TestServerErrorDetail(t *testing.T) {
	t.Parallel()
	require.Equal(t, "The OAuth2 access token could not be validated.",
		serverErrorDetail([]byte(`{"code":"x","error":"The OAuth2 access token could not be validated."}`)))
	require.Equal(t, "", serverErrorDetail([]byte("<html><body>404</body></html>")))
	require.Equal(t, "plain failure", serverErrorDetail([]byte("plain failure\nsecond line")))
	require.Equal(t, "", serverErrorDetail(nil))
}

func insecureTestConfig(wsBase string) Config {
	return Config{APIBase: wsBase, STTWSPath: "/v1/stt"}.Normalize()
}

// insecureTLS trusts the self-signed httptest server certificate.
var insecureTLS = &tls.Config{InsecureSkipVerify: true}

// sttTestServer upgrades on the Nth attempt and rejects earlier ones with
// rejectStatus, recording the bearer of every attempt.
func sttTestServer(t *testing.T, rejectStatus, rejectUntil int) (wsBase string, bearers *csync.Slice[string]) {
	t.Helper()
	bearers = csync.NewSlice[string]()
	var attempts atomic.Int32
	up := websocket.Upgrader{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearers.Append(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if int(attempts.Add(1)) <= rejectUntil {
			w.Header().Set("Content-Type", "application/json")
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
	cfg := insecureTestConfig(wsBase)

	var forced atomic.Int32
	bearerFn := func(_ context.Context, force bool) (string, error) {
		if force {
			forced.Add(1)
			return "fresh", nil
		}
		return "stale", nil
	}
	stt, err := connectWithAuth(context.Background(), cfg, bearerFn, insecureTLS)
	require.NoError(t, err)
	stt.close()
	require.Equal(t, int32(1), forced.Load())
	require.Equal(t, []string{"stale", "fresh"}, bearers.Copy())
}

func TestConnectWithAuthGivesUpAfterSecond403(t *testing.T) {
	t.Parallel()
	wsBase, bearers := sttTestServer(t, http.StatusForbidden, 99)
	cfg := insecureTestConfig(wsBase)
	bearerFn := func(_ context.Context, _ bool) (string, error) { return "tok", nil }
	_, err := connectWithAuth(context.Background(), cfg, bearerFn, insecureTLS)
	var ve *Error
	require.ErrorAs(t, err, &ve)
	require.Equal(t, ErrAuth, ve.Kind)
	require.Equal(t, http.StatusForbidden, ve.HTTPStatus)
	require.Contains(t, ve.Msg, "HTTP 403")
	require.Contains(t, ve.Msg, "token rejected")
	require.NotContains(t, ve.Msg, "sample_rate", "query string must be redacted from the error")
	require.Len(t, bearers.Copy(), 2)
}

func TestConnectWithAuthDoesNotRetryOn404(t *testing.T) {
	t.Parallel()
	wsBase, bearers := sttTestServer(t, http.StatusNotFound, 99)
	cfg := insecureTestConfig(wsBase)
	bearerFn := func(_ context.Context, _ bool) (string, error) { return "tok", nil }
	_, err := connectWithAuth(context.Background(), cfg, bearerFn, insecureTLS)
	var ve *Error
	require.ErrorAs(t, err, &ve)
	require.Equal(t, ErrWebSocket, ve.Kind)
	require.Equal(t, http.StatusNotFound, ve.HTTPStatus)
	require.Len(t, bearers.Copy(), 1)
}
