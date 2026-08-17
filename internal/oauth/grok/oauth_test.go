package grok

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/oauth"
)

func TestCredentialsFromDisk(t *testing.T) {
	dir := t.TempDir()
	authFile := filepath.Join(dir, "auth.json")
	require.NoError(t, os.WriteFile(authFile, []byte(`{
  "https://auth.x.ai::client-abc": {
    "key": "access-123",
    "auth_mode": "oidc",
    "refresh_token": "refresh-xyz",
    "oidc_issuer": "https://auth.x.ai",
    "oidc_client_id": "client-abc",
    "expires_at": "2999-01-01T00:00:00Z"
  }
}`), 0o600))

	t.Setenv("GROK_HOME", dir)

	creds, ok := CredentialsFromDisk()
	require.True(t, ok)
	require.Equal(t, "access-123", creds.AccessToken)
	require.Equal(t, "refresh-xyz", creds.RefreshToken)
	require.Equal(t, "client-abc", creds.ClientID)
	require.Equal(t, "https://auth.x.ai", creds.Issuer)
	require.False(t, creds.ExpiresAt.IsZero())
}

func TestCredentialsFromDiskMissing(t *testing.T) {
	t.Setenv("GROK_HOME", t.TempDir())
	_, ok := CredentialsFromDisk()
	require.False(t, ok)
}

func TestCredentialsFromDiskSkipsNonOIDC(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{
  "api::key": {"key": "k", "auth_mode": "api_key"}
}`), 0o600))
	t.Setenv("GROK_HOME", dir)

	_, ok := CredentialsFromDisk()
	require.False(t, ok)
}

func TestCredentialsFromDiskSkipsEmptyAccessToken(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{
  "https://auth.x.ai::c": {"key": "", "auth_mode": "oidc", "refresh_token": "r", "oidc_client_id": "c"}
}`), 0o600))
	t.Setenv("GROK_HOME", dir)

	_, ok := CredentialsFromDisk()
	require.False(t, ok)
}

func TestCredentialsFromDiskDeterministicPicksFurthestExpiry(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{
  "https://auth.x.ai::old": {"key": "old-access", "auth_mode": "oidc", "refresh_token": "old-r", "oidc_client_id": "old-c", "expires_at": "2020-01-01T00:00:00Z"},
  "https://auth.x.ai::new": {"key": "new-access", "auth_mode": "oidc", "refresh_token": "new-r", "oidc_client_id": "new-c", "expires_at": "2999-01-01T00:00:00Z"}
}`), 0o600))
	t.Setenv("GROK_HOME", dir)

	for range 20 {
		creds, ok := CredentialsFromDisk()
		require.True(t, ok)
		require.Equal(t, "new-access", creds.AccessToken)
		require.Equal(t, "new-c", creds.ClientID)
	}
}

func TestRefreshTokenRequiresRefreshToken(t *testing.T) {
	t.Parallel()

	_, err := RefreshToken(t.Context(), &oauth.Token{})
	require.Error(t, err)
}

func TestRefreshToken(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
		require.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
		require.Equal(t, "client-abc", r.Form.Get("client_id"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`))
	}))
	defer srv.Close()

	tok := &oauth.Token{
		RefreshToken: "old-refresh",
		Client:       &oauth.OAuthClient{ClientID: "client-abc", TokenURL: srv.URL},
	}

	refreshed, err := RefreshToken(t.Context(), tok)
	require.NoError(t, err)
	require.Equal(t, "new-access", refreshed.AccessToken)
	require.Equal(t, "new-refresh", refreshed.RefreshToken)
	require.Equal(t, "client-abc", refreshed.Client.ClientID)
	require.Equal(t, srv.URL, refreshed.Client.TokenURL)
	require.Greater(t, refreshed.ExpiresAt, int64(0))
}

func TestRefreshTokenKeepsRefreshTokenWhenOmitted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","expires_in":3600}`))
	}))
	defer srv.Close()

	tok := &oauth.Token{
		RefreshToken: "keep-me",
		Client:       &oauth.OAuthClient{ClientID: "client-abc", TokenURL: srv.URL},
	}

	refreshed, err := RefreshToken(t.Context(), tok)
	require.NoError(t, err)
	require.Equal(t, "keep-me", refreshed.RefreshToken)
}

func TestRefreshTokenDefaultsExpiryWhenOmitted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No expires_in in the response.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"r"}`))
	}))
	defer srv.Close()

	tok := &oauth.Token{
		RefreshToken: "r",
		Client:       &oauth.OAuthClient{ClientID: "client-abc", TokenURL: srv.URL},
	}

	refreshed, err := RefreshToken(t.Context(), tok)
	require.NoError(t, err)
	// Must not be treated as immediately expired (which would cause a
	// refresh storm).
	require.False(t, refreshed.IsExpired())
	require.Greater(t, refreshed.ExpiresAt, int64(0))
}

func TestRefreshTokenOmitsScope(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		// Scope must be omitted on refresh so a superset scope cannot
		// trigger invalid_scope against the grok CLI's client_id.
		require.Empty(t, r.Form.Get("scope"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r","expires_in":3600}`))
	}))
	defer srv.Close()

	tok := &oauth.Token{
		RefreshToken: "r",
		Client:       &oauth.OAuthClient{ClientID: "client-abc", TokenURL: srv.URL},
	}
	_, err := RefreshToken(t.Context(), tok)
	require.NoError(t, err)
}

func TestHeaders(t *testing.T) {
	t.Parallel()

	h := Headers()
	require.NotEmpty(t, h["x-grok-client-version"])
	require.NotEmpty(t, h["x-grok-client-identifier"])
}
