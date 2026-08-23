package grok

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/oauth"
)

// LoginSession is an in-progress browser authorization-code + PKCE flow.
// Start it with [StartLogin], send the user to AuthURL, then call
// [LoginSession.Wait] to block until the loopback redirect delivers the
// authorization code and it is exchanged for a token.
type LoginSession struct {
	// AuthURL is the authorization URL the user must open in a browser.
	AuthURL string

	clientID     string
	tokenURL     string
	redirectURI  string
	codeVerifier string
	state        string
	listener     net.Listener
}

// StartLogin discovers the xAI OIDC endpoints, binds a loopback callback
// server on 127.0.0.1, and builds the authorization URL for the PKCE
// flow. The returned session owns the listener until [LoginSession.Wait]
// or [LoginSession.Close] is called. This mirrors the official Grok CLI
// login and needs no pre-existing grok CLI credentials.
func StartLogin(ctx context.Context) (*LoginSession, error) {
	authEndpoint, tokenEndpoint := discoverEndpoints(ctx, defaultIssuer)
	return newLoginSession(ctx, authEndpoint, tokenEndpoint)
}

// newLoginSession builds a login session against explicit endpoints. It
// is split from [StartLogin] so tests can supply local endpoints without
// running OIDC discovery over the network.
func newLoginSession(ctx context.Context, authEndpoint, tokenEndpoint string) (*LoginSession, error) {
	// Loopback redirect on an OS-assigned port, matching the Grok CLI's
	// http://127.0.0.1:<port>/callback contract.
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("grok: could not start callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	verifier, challenge, err := generatePKCE()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	state, err := randomToken()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	nonce, err := randomToken()
	if err != nil {
		_ = listener.Close()
		return nil, err
	}

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", oauthClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", scope)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("nonce", nonce)

	return &LoginSession{
		AuthURL:      authEndpoint + "?" + q.Encode(),
		clientID:     oauthClientID,
		tokenURL:     tokenEndpoint,
		redirectURI:  redirectURI,
		codeVerifier: verifier,
		state:        state,
		listener:     listener,
	}, nil
}

// Close releases the callback listener without waiting. Safe to call
// after Wait (which closes it too).
func (s *LoginSession) Close() {
	if s.listener != nil {
		_ = s.listener.Close()
	}
}

// callbackResult carries the outcome of the loopback redirect from the
// HTTP handler goroutine back to Wait.
type callbackResult struct {
	code  string
	state string
	err   error
}

// Wait serves the loopback callback until the IdP redirects with an
// authorization code (or ctx is cancelled), validates the state, and
// exchanges the code for a token. The callback listener is always closed
// before returning.
func (s *LoginSession) Wait(ctx context.Context) (*oauth.Token, error) {
	defer s.Close()

	resultCh := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		errStr := q.Get("error")
		code := q.Get("code")
		// Ignore stray requests (favicon probes, browser prefetch, port
		// scans) that carry neither a code nor an error, so they cannot
		// latch the single-slot result channel and shadow the genuine
		// redirect that arrives afterward.
		if errStr == "" && code == "" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errStr != "" {
			desc := q.Get("error_description")
			writeCallbackPage(w, false)
			select {
			case resultCh <- callbackResult{err: fmt.Errorf("grok: authorization failed: %s %s", errStr, desc)}:
			default:
			}
			return
		}
		writeCallbackPage(w, true)
		select {
		case resultCh <- callbackResult{code: code, state: q.Get("state")}:
		default:
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(s.listener) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-resultCh:
		if res.err != nil {
			return nil, res.err
		}
		if res.state != s.state {
			return nil, fmt.Errorf("grok: state mismatch (possible CSRF); expected %q got %q", s.state, res.state)
		}
		if res.code == "" {
			return nil, fmt.Errorf("grok: callback missing authorization code")
		}
		return s.exchangeCode(ctx, res.code)
	}
}

// exchangeCode swaps the authorization code for an access/refresh token
// pair at the discovered token endpoint, preserving the client metadata
// so a later [RefreshToken] can run without the browser flow.
func (s *LoginSession) exchangeCode(ctx context.Context, code string) (*oauth.Token, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.redirectURI)
	form.Set("client_id", s.clientID)
	form.Set("code_verifier", s.codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL, strings.NewReader(form.Encode()))
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
		return nil, fmt.Errorf("grok: code exchange failed: status %d body %q", resp.StatusCode, string(body))
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
	// expires_in is OPTIONAL per RFC 6749. Without it, SetExpiresAt would
	// mark the fresh token immediately expired and every request would
	// trigger a network refresh. Fall back to a conservative lifetime.
	if result.ExpiresIn <= 0 {
		result.ExpiresIn = defaultAccessTokenTTL
	}

	token := &oauth.Token{
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
		Client: &oauth.OAuthClient{
			ClientID: s.clientID,
			TokenURL: s.tokenURL,
		},
	}
	token.SetExpiresAt()
	return token, nil
}

// generatePKCE produces a PKCE verifier/challenge pair using S256,
// matching the Grok CLI: verifier is 32 random bytes base64url-encoded
// (no padding), challenge is the base64url SHA-256 of the verifier.
func generatePKCE() (verifier, challenge string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("grok: could not generate PKCE verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// randomToken returns a URL-safe random string for state/nonce values.
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("grok: could not generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// writeCallbackPage renders a minimal browser response the user sees
// after the IdP redirect completes.
func writeCallbackPage(w http.ResponseWriter, ok bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if ok {
		_, _ = io.WriteString(w, "<html><body><h2>Authentication complete.</h2><p>You can close this window and return to Crush.</p></body></html>")
		return
	}
	w.WriteHeader(http.StatusBadRequest)
	_, _ = io.WriteString(w, "<html><body><h2>Authentication failed.</h2><p>Return to Crush for details.</p></body></html>")
}
