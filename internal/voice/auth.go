package voice

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
)

const grokProviderID = "grok"

const notSignedInMsg = "not signed in — run `crush login grok`, set XAI_API_KEY, or set a grok provider api_key"

// TokenRefresher refreshes a provider's OAuth token and persists it.
// Satisfied by workspace.Workspace.
type TokenRefresher interface {
	RefreshOAuthToken(ctx context.Context, scope config.Scope, providerID string) error
}

// BearerFunc resolves the STT bearer for one connection attempt. When
// forceRefresh is set the caller has just received a 401/403 with the
// previous token and wants a freshly minted one.
type BearerFunc func(ctx context.Context, forceRefresh bool) (string, error)

// NewBearerFunc builds a [BearerFunc] that follows a rotating grok OAuth
// token. cfgFn is called on every resolve so a token refreshed elsewhere
// (agent 401 retry, another crush process) is picked up; refresher may
// be nil, in which case expired tokens are returned as-is.
func NewBearerFunc(cfgFn func() *config.Config, refresher TokenRefresher, voice Config) BearerFunc {
	return func(ctx context.Context, forceRefresh bool) (string, error) {
		if key := strings.TrimSpace(voice.APIKey); key != "" {
			return key, nil
		}
		if key := strings.TrimSpace(os.Getenv("XAI_API_KEY")); key != "" {
			return key, nil
		}
		cfg := cfgFn()
		pc, ok := grokProvider(cfg)
		if !ok {
			return "", authErr(notSignedInMsg)
		}
		expired := pc.OAuthToken != nil && pc.OAuthToken.ExpiresAt > 0 && pc.OAuthToken.IsExpired()
		if pc.OAuthToken != nil && refresher != nil && (forceRefresh || expired) {
			if err := refresher.RefreshOAuthToken(ctx, config.ScopeGlobal, pc.ID); err != nil {
				slog.Warn("Failed to refresh grok OAuth token for voice", "error", err)
				if forceRefresh {
					return "", authErr("could not refresh grok OAuth token — run `crush login grok`")
				}
			} else if refreshed, ok := grokProvider(cfgFn()); ok {
				pc = refreshed
			}
		}
		return bearerFromProvider(pc)
	}
}

func grokProvider(cfg *config.Config) (config.ProviderConfig, bool) {
	if cfg == nil || cfg.Providers == nil {
		return config.ProviderConfig{}, false
	}
	pc, ok := cfg.Providers.Get(grokProviderID)
	if !ok {
		pc, ok = cfg.Providers.Get(string(catwalk.InferenceProviderGrok))
	}
	if ok && pc.ID == "" {
		pc.ID = grokProviderID
	}
	return pc, ok
}

func bearerFromProvider(pc config.ProviderConfig) (string, error) {
	if pc.OAuthToken != nil && strings.TrimSpace(pc.OAuthToken.AccessToken) != "" {
		return pc.OAuthToken.AccessToken, nil
	}
	if key := strings.TrimSpace(pc.APIKey); key != "" {
		return key, nil
	}
	return "", authErr(notSignedInMsg)
}

// ResolveAPIBase returns the STT API base: the dedicated voice api_base
// when set, else the default. The grok provider's base_url is
// deliberately not inherited: the subscription proxy
// (cli-chat-proxy.grok.com) only serves chat and 404s on /v1/stt, while
// api.x.ai accepts the same OAuth bearer directly.
func ResolveAPIBase(voice Config) string {
	if base := strings.TrimSpace(voice.APIBase); base != "" {
		return strings.TrimRight(base, "/")
	}
	return defaultAPIBase
}
