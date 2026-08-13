// Package grok provides authentication against the Grok CLI subscription
// backend (cli-chat-proxy.grok.com), reusing the OIDC credentials created
// by the official `grok` CLI and refreshing them via the xAI IdP.
package grok

import (
	"encoding/json"
	"os"
	"path/filepath"
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
	return filepath.Join(os.Getenv("HOME"), ".grok", "auth.json")
}

// CredentialsFromDisk reads the Grok CLI auth file and returns the first
// usable OIDC credential (one carrying a refresh token and client ID). The
// bool is false when no such credential is present.
func CredentialsFromDisk() (*Credentials, bool) {
	data, err := os.ReadFile(AuthFilePath())
	if err != nil {
		return nil, false
	}
	var entries map[string]authFileEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, false
	}
	for _, e := range entries {
		if e.RefreshToken == "" || e.OIDCClientID == "" {
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
		if t, err := time.Parse(time.RFC3339, e.ExpiresAt); err == nil {
			creds.ExpiresAt = t
		}
		return creds, true
	}
	return nil, false
}
