// Package grok provides authentication against the Grok CLI subscription
// backend (cli-chat-proxy.grok.com), reusing the OIDC credentials created
// by the official `grok` CLI and refreshing them via the xAI IdP.
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Credentials holds the subset of a Grok CLI auth entry needed to
// authenticate and refresh against the xAI IdP.
type Credentials struct {
	AccessToken  string
	RefreshToken string
	ClientID     string
	Issuer       string
	ExpiresAt    time.Time
}

// authFileEntry mirrors a single entry in ~/.grok/auth.json.
type authFileEntry struct {
	Key          string `json:"key"`
	AuthMode     string `json:"auth_mode"`
	RefreshToken string `json:"refresh_token"`
	OIDCIssuer   string `json:"oidc_issuer"`
	OIDCClientID string `json:"oidc_client_id"`
	ExpiresAt    string `json:"expires_at"`
}

// AuthFilePath returns the path to the Grok CLI auth file.
func AuthFilePath() string {
	if dir := os.Getenv("GROK_HOME"); dir != "" {
		return filepath.Join(dir, "auth.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, ".grok", "auth.json")
}

// CredentialsFromDisk reads the Grok CLI auth file and returns the most
// recently-expiring usable OIDC credential (one carrying an access token,
// refresh token and client ID). Selection is deterministic: candidates
// are ranked by expiry (furthest-future first) with the auth-file key as
// a stable tie-break, so multiple logged-in accounts never produce a
// coin-flip result. The bool is false when no usable credential exists.
func CredentialsFromDisk() (*Credentials, bool) {
	data, err := os.ReadFile(AuthFilePath())
	if err != nil {
		return nil, false
	}
	var entries map[string]authFileEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, false
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var best *Credentials
	var bestExpiry time.Time
	for _, k := range keys {
		e := entries[k]
		if e.Key == "" || e.RefreshToken == "" || e.OIDCClientID == "" {
			continue
		}
		if e.AuthMode != "" && e.AuthMode != "oidc" {
			continue
		}
		creds := &Credentials{
			AccessToken:  e.Key,
			RefreshToken: e.RefreshToken,
			ClientID:     e.OIDCClientID,
			Issuer:       strings.TrimRight(e.OIDCIssuer, "/"),
		}
		if creds.Issuer == "" {
			creds.Issuer = defaultIssuer
		}
		var expiry time.Time
		if t, err := time.Parse(time.RFC3339, e.ExpiresAt); err == nil {
			creds.ExpiresAt = t
			expiry = t
		}
		// Prefer the credential whose access token lives longest; keys
		// are already sorted, so equal expiries resolve deterministically
		// to the first key.
		if best == nil || expiry.After(bestExpiry) {
			best = creds
			bestExpiry = expiry
		}
	}
	if best == nil {
		return nil, false
	}
	return best, true
}
