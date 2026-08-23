package grok

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeneratePKCE(t *testing.T) {
	t.Parallel()

	verifier, challenge, err := generatePKCE()
	require.NoError(t, err)
	require.NotEmpty(t, verifier)
	require.NotEmpty(t, challenge)

	// challenge must be base64url(sha256(verifier)), no padding.
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	require.Equal(t, want, challenge)

	// Distinct invocations produce distinct verifiers.
	verifier2, _, err := generatePKCE()
	require.NoError(t, err)
	require.NotEqual(t, verifier, verifier2)
}

func TestStartLoginBuildsAuthURL(t *testing.T) {
	t.Parallel()

	session, err := newLoginSession(context.Background(), defaultAuthURL, defaultTokenURL)
	require.NoError(t, err)
	defer session.Close()

	u, err := url.Parse(session.AuthURL)
	require.NoError(t, err)
	q := u.Query()
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, oauthClientID, q.Get("client_id"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.NotEmpty(t, q.Get("code_challenge"))
	require.NotEmpty(t, q.Get("state"))
	require.NotEmpty(t, q.Get("nonce"))
	require.Equal(t, scope, q.Get("scope"))
	require.True(t, strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:"))
	require.True(t, strings.HasSuffix(q.Get("redirect_uri"), "/callback"))
}

func TestLoginSessionExchangeCode(t *testing.T) {
	t.Parallel()

	var gotForm url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = r.Form
		require.Equal(t, clientVersion, r.Header.Get("x-grok-client-version"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"acc","refresh_token":"ref","expires_in":3600}`))
	}))
	defer srv.Close()

	session := &LoginSession{
		clientID:     oauthClientID,
		tokenURL:     srv.URL,
		redirectURI:  "http://127.0.0.1:12345/callback",
		codeVerifier: "verifier-abc",
	}

	token, err := session.exchangeCode(context.Background(), "auth-code-1")
	require.NoError(t, err)
	require.Equal(t, "acc", token.AccessToken)
	require.Equal(t, "ref", token.RefreshToken)
	require.Equal(t, oauthClientID, token.Client.ClientID)
	require.Equal(t, srv.URL, token.Client.TokenURL)
	require.Greater(t, token.ExpiresAt, int64(0))

	require.Equal(t, "authorization_code", gotForm.Get("grant_type"))
	require.Equal(t, "auth-code-1", gotForm.Get("code"))
	require.Equal(t, "http://127.0.0.1:12345/callback", gotForm.Get("redirect_uri"))
	require.Equal(t, oauthClientID, gotForm.Get("client_id"))
	require.Equal(t, "verifier-abc", gotForm.Get("code_verifier"))
}

func TestLoginSessionWaitRejectsStateMismatch(t *testing.T) {
	t.Parallel()

	session, err := newLoginSession(context.Background(), defaultAuthURL, defaultTokenURL)
	require.NoError(t, err)

	u, err := url.Parse(session.AuthURL)
	require.NoError(t, err)
	redirectURI := u.Query().Get("redirect_uri")

	// Fire a callback with a wrong state once Wait is serving.
	errCh := make(chan error, 1)
	go func() {
		_, err := session.Wait(context.Background())
		errCh <- err
	}()

	require.Eventually(t, func() bool {
		resp, err := http.Get(redirectURI + "?code=abc&state=WRONG")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return true
	}, 2e9, 2e7)

	require.ErrorContains(t, <-errCh, "state mismatch")
}
