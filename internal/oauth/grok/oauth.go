package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/oauth"
)

const (
	// defaultIssuer is the xAI OIDC issuer used when the auth file does
	// not record one.
	defaultIssuer = "https://auth.x.ai"
	// defaultAuthURL is the OIDC authorization endpoint used when
	// discovery fails or has not run yet.
	defaultAuthURL = defaultIssuer + "/oauth2/auth"
	// defaultTokenURL is the OIDC token endpoint used when discovery
	// fails or has not run yet.
	defaultTokenURL = defaultIssuer + "/oauth2/token"
	// scope requested on the browser flow and refresh, matching the
	// frozen scope set the Grok CLI uses (grok-build config.rs
	// default_oauth2_scopes).
	scope = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write workspaces:read workspaces:write"

	// oauthClientID is the public OIDC client the Grok CLI registers for
	// the loopback authorization-code + PKCE flow (grok-build config.rs).
	oauthClientID = "b1a00492-073a-47ea-816f-4c329264a828"

	// clientVersion is reported to the chat proxy via x-grok-client-version.
	// The proxy rejects requests without a sufficiently recent version.
	clientVersion    = "1.0.3"
	clientIdentifier = "grok-shell"
	clientMode       = "cli"
	userAgent        = "xai-grok-cli/" + clientVersion

	// defaultAccessTokenTTL is the lifetime assumed for a refreshed
	// access token when the IdP omits expires_in, so the token is not
	// treated as immediately expired.
	defaultAccessTokenTTL = 3600
)

// Headers returns the client-identifying headers the Grok subscription
// proxy requires on every request.
func Headers() map[string]string {
	return map[string]string{
		"User-Agent":               userAgent,
		"x-grok-client-version":    clientVersion,
		"x-grok-client-identifier": clientIdentifier,
		"x-grok-client-mode":       clientMode,
	}
}

// TokenFromCredentials builds an oauth.Token from disk credentials,
// stashing the client ID and token endpoint so a later refresh can run
// without re-reading the Grok CLI auth file.
func TokenFromCredentials(ctx context.Context, creds *Credentials) *oauth.Token {
	tokenURL := discoverTokenURL(ctx, creds.Issuer)
	tok := &oauth.Token{
		AccessToken:  creds.AccessToken,
		RefreshToken: creds.RefreshToken,
		Client: &oauth.OAuthClient{
			ClientID: creds.ClientID,
			TokenURL: tokenURL,
		},
	}
	if !creds.ExpiresAt.IsZero() {
		tok.ExpiresAt = creds.ExpiresAt.Unix()
		tok.SetExpiresIn()
	} else {
		tok.SetExpiresAt()
	}
	return tok
}

// RefreshToken exchanges the token's refresh token for a fresh access
// token using the xAI OIDC token endpoint. The returned token preserves
// the client metadata needed for future refreshes.
func RefreshToken(ctx context.Context, token *oauth.Token) (*oauth.Token, error) {
	if token == nil || token.RefreshToken == "" {
		return nil, fmt.Errorf("grok: no refresh token available")
	}
	clientID := ""
	tokenURL := defaultTokenURL
	if token.Client != nil {
		if token.Client.ClientID != "" {
			clientID = token.Client.ClientID
		}
		if token.Client.TokenURL != "" {
			tokenURL = token.Client.TokenURL
		}
	}
	if clientID == "" {
		return nil, fmt.Errorf("grok: missing OIDC client ID for refresh")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", token.RefreshToken)
	form.Set("client_id", clientID)
	// Deliberately omit scope: RFC 6749 §6 defaults a refresh to the
	// originally-granted scope, and sending Crush's superset scope with
	// the grok CLI's client_id (which may have been granted a narrower
	// set) would trigger invalid_scope and break the disk-import flow.

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-grok-client-version", clientVersion)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("grok: token refresh failed: status %d body %q", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("grok: could not decode token response: %w", err)
	}
	if result.AccessToken == "" {
		return nil, fmt.Errorf("grok: token response missing access_token")
	}

	refreshed := &oauth.Token{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		Client: &oauth.OAuthClient{
			ClientID: clientID,
			TokenURL: tokenURL,
		},
	}
	// IdPs that rotate refresh tokens omit the field on reuse; keep the
	// previous one so the credential stays refreshable.
	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = token.RefreshToken
	}
	// expires_in is OPTIONAL per RFC 6749. Without it, SetExpiresAt would
	// mark the token immediately expired and every request would trigger
	// another network refresh. Fall back to a conservative lifetime.
	if refreshed.ExpiresIn <= 0 {
		refreshed.ExpiresIn = defaultAccessTokenTTL
	}
	refreshed.SetExpiresAt()
	return refreshed, nil
}

// discoverTokenURL fetches the OIDC discovery document to resolve the
// token endpoint, falling back to the well-known default on any failure.
func discoverTokenURL(ctx context.Context, issuer string) string {
	_, tokenURL := discoverEndpoints(ctx, issuer)
	return tokenURL
}

// discoverEndpoints fetches the OIDC discovery document to resolve the
// authorization and token endpoints, falling back to the well-known
// defaults on any failure.
func discoverEndpoints(ctx context.Context, issuer string) (authURL, tokenURL string) {
	if issuer == "" {
		issuer = defaultIssuer
	}
	authURL, tokenURL = defaultAuthURL, defaultTokenURL
	discoveryURL := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return authURL, tokenURL
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return authURL, tokenURL
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return authURL, tokenURL
	}
	var doc struct {
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return authURL, tokenURL
	}
	if doc.AuthorizationEndpoint != "" {
		authURL = doc.AuthorizationEndpoint
	}
	if doc.TokenEndpoint != "" {
		tokenURL = doc.TokenEndpoint
	}
	return authURL, tokenURL
}
