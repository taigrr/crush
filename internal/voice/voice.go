// Package voice mints short-lived realtime voice credentials for clients
// (docs/specs/MOBILE_DEVICE_BRIDGE.md, Voice).
package voice

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultBaseURL is the xAI API base URL.
const DefaultBaseURL = "https://api.x.ai/v1"

// DefaultTTL is how long a minted secret stays valid.
const DefaultTTL = 5 * time.Minute

// maxResponse bounds the xAI response body.
const maxResponse = 64 << 10

// ErrNoCredential reports that no xAI credential is configured.
var ErrNoCredential = errors.New("no xAI credential configured for voice: set options.voice.api_key, XAI_API_KEY, or log in to Grok")

// Credential authenticates the mint request. It is never logged.
type Credential struct {
	// Source names where the credential came from (for diagnostics; it
	// never contains the credential itself).
	Source  string
	Token   string
	Headers map[string]string
}

// Secret is a minted ephemeral client secret.
type Secret struct {
	Value     string
	ExpiresAt time.Time
}

// Minter mints xAI realtime client secrets.
type Minter struct {
	BaseURL string
	Client  *http.Client
	TTL     time.Duration
	Now     func() time.Time
}

type mintRequest struct {
	ExpiresAfter struct {
		Seconds int `json:"seconds"`
	} `json:"expires_after"`
}

type secretBody struct {
	Value     string `json:"value"`
	ExpiresAt int64  `json:"expires_at"`
}

type mintResponse struct {
	secretBody
	// Some realtime APIs nest the secret.
	ClientSecret *secretBody `json:"client_secret"`
}

// Mint requests an ephemeral secret with cred.
func (m *Minter) Mint(ctx context.Context, cred Credential) (Secret, error) {
	if strings.TrimSpace(cred.Token) == "" {
		return Secret{}, ErrNoCredential
	}
	ttl := cmp.Or(m.TTL, DefaultTTL)
	var body mintRequest
	body.ExpiresAfter.Seconds = int(ttl.Seconds())
	buf, err := json.Marshal(body)
	if err != nil {
		return Secret{}, err
	}
	url := strings.TrimSuffix(cmp.Or(m.BaseURL, DefaultBaseURL), "/") + "/realtime/client_secrets"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return Secret{}, err
	}
	for k, v := range cred.Headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cred.Token)
	client := m.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Secret{}, fmt.Errorf("mint voice secret: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return Secret{}, fmt.Errorf("mint voice secret: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		// The body is not echoed: it could reflect request material.
		return Secret{}, &UpstreamError{Status: resp.StatusCode}
	}
	var out mintResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Secret{}, fmt.Errorf("mint voice secret: decode response: %w", err)
	}
	sec := out.secretBody
	if sec.Value == "" && out.ClientSecret != nil {
		sec = *out.ClientSecret
	}
	if sec.Value == "" {
		return Secret{}, errors.New("mint voice secret: response has no value")
	}
	exp := time.Unix(sec.ExpiresAt, 0)
	if sec.ExpiresAt == 0 {
		now := time.Now
		if m.Now != nil {
			now = m.Now
		}
		exp = now().Add(ttl)
	}
	return Secret{Value: sec.Value, ExpiresAt: exp}, nil
}

// UpstreamError is a non-2xx response from xAI.
type UpstreamError struct{ Status int }

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("xAI refused the voice secret request (HTTP %d)", e.Status)
}
