package backend

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/oauth"
)

func TestVoiceCredentialOrder(t *testing.T) {
	t.Parallel()
	withProviders := func(pcs ...config.ProviderConfig) *config.Config {
		m := csync.NewMap[string, config.ProviderConfig]()
		for _, pc := range pcs {
			m.Set(pc.ID, pc)
		}
		return &config.Config{Providers: m, Options: &config.Options{}}
	}
	grokOAuth := config.ProviderConfig{ID: "grok", APIKey: "fake-grok-access", OAuthToken: &oauth.Token{AccessToken: "fake-grok-access"}}
	xai := config.ProviderConfig{ID: "xai", APIKey: "fake-xai-provider"}
	grokKey := config.ProviderConfig{ID: "grok", APIKey: "fake-grok-key"}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "XAI_API_KEY" {
				return v
			}
			return ""
		}
	}

	tests := []struct {
		name   string
		cfg    *config.Config
		env    string
		want   string
		source string
	}{
		{"voice key wins", func() *config.Config {
			c := withProviders(grokOAuth, xai)
			c.Options.Voice = &config.VoiceOptions{APIKey: "fake-voice"}
			return c
		}(), "fake-env", "fake-voice", "options.voice.api_key"},
		{"env next", withProviders(grokOAuth, xai), "fake-env", "fake-env", "XAI_API_KEY"},
		{"grok oauth next", withProviders(grokOAuth, xai), "", "fake-grok-access", "grok oauth"},
		{"xai provider next", withProviders(xai, grokKey), "", "fake-xai-provider", "xai provider"},
		{"grok provider key last", withProviders(grokKey), "", "fake-grok-key", "grok provider"},
		{"unresolved template skipped", withProviders(config.ProviderConfig{ID: "xai", APIKey: "$XAI_API_KEY"}), "", "", ""},
		{"none", withProviders(), "", "", ""},
		{"nil config", nil, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cred, ok := voiceCredential(tt.cfg, config.IdentityResolver(), env(tt.env))
			require.Equal(t, tt.want != "", ok)
			require.Equal(t, tt.want, cred.Token)
			require.Equal(t, tt.source, cred.Source)
		})
	}
}

func TestProviderInfosRedacted(t *testing.T) {
	t.Parallel()
	m := csync.NewMap[string, config.ProviderConfig]()
	m.Set("openai", config.ProviderConfig{
		ID: "openai", APIKey: "sk-fake-SECRET",
		OAuthToken:   &oauth.Token{AccessToken: "fake-SECRET-access", RefreshToken: "fake-SECRET-refresh"},
		ExtraHeaders: map[string]string{"Authorization": "Bearer fake-SECRET"},
		ExtraBody:    map[string]any{"token": "fake-SECRET"},
		BaseURL:      "https://user:fake-SECRET@example.com",
	})
	m.Set("custom", config.ProviderConfig{ID: "custom", Name: "Custom", Models: []catwalk.Model{{ID: "c1"}}})
	catalog := []catwalk.Provider{
		{ID: "openai", Name: "OpenAI", APIKey: "$OPENAI_API_KEY", DefaultHeaders: map[string]string{"X": "fake-SECRET"}, Models: []catwalk.Model{{ID: "gpt"}}},
		{ID: "anthropic", Name: "Anthropic", Models: []catwalk.Model{{ID: "claude"}}},
	}
	infos := providerInfos(&config.Config{Providers: m}, catalog)
	raw, err := json.Marshal(infos)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET")
	require.NotContains(t, string(raw), "OPENAI_API_KEY")
	require.Len(t, infos, 3)
	require.Equal(t, "openai", infos[0].ID, "configured first, catalog order")
	require.True(t, infos[0].HasCredential)
	require.True(t, infos[0].OAuth)
	require.Equal(t, "gpt", infos[0].Models[0].ID)
	require.Equal(t, "custom", infos[1].ID)
	require.False(t, infos[1].HasCredential)
	require.Equal(t, "anthropic", infos[2].ID)
	require.False(t, infos[2].Configured)
}
