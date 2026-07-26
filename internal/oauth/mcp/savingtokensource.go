package mcpoauth

import (
	"log/slog"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// NewSavingTokenSource wraps an oauth2.TokenSource and calls saver whenever
// the access token changes (i.e. on refresh). This ensures refreshed tokens
// are persisted automatically without re-prompting the user.
//
// Returns nil if wrapped is nil. Returns wrapped directly if saver is nil.
func NewSavingTokenSource(wrapped oauth2.TokenSource, config *oauth2.Config, initialToken *oauth2.Token, saver func(*oauth2.Config, *oauth2.Token)) oauth2.TokenSource {
	if wrapped == nil {
		return nil
	}
	if saver == nil {
		return wrapped
	}
	var accessToken, refreshToken string
	var expiry time.Time
	if initialToken != nil {
		accessToken = initialToken.AccessToken
		refreshToken = initialToken.RefreshToken
		expiry = initialToken.Expiry
	}
	return &savingTokenSource{
		src:          wrapped,
		saver:        saver,
		config:       config,
		accessToken:  accessToken,
		refreshToken: refreshToken,
		expiry:       expiry,
	}
}

type savingTokenSource struct {
	mu           sync.Mutex
	src          oauth2.TokenSource
	saver        func(*oauth2.Config, *oauth2.Token)
	config       *oauth2.Config
	accessToken  string
	refreshToken string
	expiry       time.Time
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := s.src.Token()
	if err != nil {
		slog.Debug("Token refresh failed", "error", err)
		return nil, err
	}
	s.mu.Lock()
	// Persist whenever any persisted field changes. Comparing only the
	// access token would miss refresh-token rotation (some providers issue
	// a new refresh token while returning the same access token), which
	// would strand the stale refresh token across restarts.
	changed := s.accessToken != tok.AccessToken ||
		s.refreshToken != tok.RefreshToken ||
		!s.expiry.Equal(tok.Expiry)
	if changed {
		s.accessToken = tok.AccessToken
		s.refreshToken = tok.RefreshToken
		s.expiry = tok.Expiry
	}
	s.mu.Unlock()
	if changed {
		s.saver(s.config, tok)
	}
	return tok, nil
}
