package backend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/voice"
)

// ErrVoiceBadRequest reports a malformed voice secret request.
var ErrVoiceBadRequest = errors.New("invalid voice request")

// SetVoiceMinter replaces the realtime voice secret minter (tests point
// it at a fake xAI server).
func (b *Backend) SetVoiceMinter(m *voice.Minter) { b.voiceMinter.Store(m) }

// SetVoiceEnv replaces the environment lookup used to resolve the voice
// credential (tests use a fake environment).
func (b *Backend) SetVoiceEnv(getenv func(string) string) { b.voiceEnv.Store(&getenv) }

// VoiceClientSecret mints a short-lived xAI realtime secret for a client
// working on sessionID, authenticated with this node's Grok credential.
// The credential and the secret are never logged.
func (b *Backend) VoiceClientSecret(ctx context.Context, sessionID string) (voice.Secret, error) {
	if strings.TrimSpace(sessionID) == "" {
		return voice.Secret{}, fmt.Errorf("%w: session_id is required", ErrVoiceBadRequest)
	}
	store := b.voiceConfigStore(ctx, sessionID)
	if store == nil {
		return voice.Secret{}, voice.ErrNoCredential
	}
	cfg := store.Config()
	getenv := os.Getenv
	if p := b.voiceEnv.Load(); p != nil {
		getenv = *p
	}
	cred, ok := voiceCredential(cfg, store.Resolver(), getenv)
	if !ok {
		return voice.Secret{}, voice.ErrNoCredential
	}
	m := voice.Minter{}
	if p := b.voiceMinter.Load(); p != nil {
		m = *p
	}
	if cfg != nil && cfg.Options != nil && cfg.Options.Voice != nil && cfg.Options.Voice.BaseURL != "" {
		m.BaseURL = cfg.Options.Voice.BaseURL
	}
	return m.Mint(ctx, cred)
}

// voiceConfigStore is the config of the workspace holding sessionID,
// else the server's own.
func (b *Backend) voiceConfigStore(ctx context.Context, sessionID string) *config.ConfigStore {
	for _, ws := range b.workspaces.Seq2() {
		if ws.Sessions == nil || ws.Cfg == nil {
			continue
		}
		if _, err := ws.Sessions.Get(ctx, sessionID); err == nil {
			return ws.Cfg
		}
	}
	if b.cfg != nil {
		return b.cfg
	}
	for _, ws := range b.workspaces.Seq2() {
		if ws.Cfg != nil {
			return ws.Cfg
		}
	}
	return nil
}

// voiceCredential resolves the xAI credential in order: the voice config
// key, XAI_API_KEY, the Grok OAuth token, then the xai or grok provider
// api_key.
func voiceCredential(cfg *config.Config, resolver config.VariableResolver, getenv func(string) string) (voice.Credential, bool) {
	if cfg != nil && cfg.Options != nil && cfg.Options.Voice != nil && cfg.Options.Voice.APIKey != "" {
		key := cfg.Options.Voice.APIKey
		if resolver != nil {
			if v, err := resolver.ResolveValue(key); err == nil {
				key = v
			} else {
				key = ""
			}
		}
		if strings.TrimSpace(key) != "" {
			return voice.Credential{Source: "options.voice.api_key", Token: strings.TrimSpace(key)}, true
		}
	}
	if key := strings.TrimSpace(getenv("XAI_API_KEY")); key != "" {
		return voice.Credential{Source: "XAI_API_KEY", Token: key}, true
	}
	if cfg == nil || cfg.Providers == nil {
		return voice.Credential{}, false
	}
	grok, hasGrok := cfg.Providers.Get(string(catwalk.InferenceProviderGrok))
	if hasGrok && grok.OAuthToken != nil && grok.OAuthToken.AccessToken != "" {
		return voice.Credential{Source: "grok oauth", Token: grok.OAuthToken.AccessToken, Headers: grok.ExtraHeaders}, true
	}
	if xai, ok := cfg.Providers.Get(string(catwalk.InferenceProviderXAI)); ok && strings.TrimSpace(xai.APIKey) != "" && !strings.HasPrefix(xai.APIKey, "$") {
		return voice.Credential{Source: "xai provider", Token: strings.TrimSpace(xai.APIKey)}, true
	}
	if hasGrok && strings.TrimSpace(grok.APIKey) != "" && !strings.HasPrefix(grok.APIKey, "$") {
		return voice.Credential{Source: "grok provider", Token: strings.TrimSpace(grok.APIKey)}, true
	}
	return voice.Credential{}, false
}
