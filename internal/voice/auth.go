package voice

import (
	"os"
	"strings"

	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/config"
)

const grokProviderID = "grok"

// ResolveBearer returns a bearer token for xAI STT. Preference order:
// dedicated [Config.APIKey], XAI_API_KEY, then the grok provider's
// API key / OAuth access token.
func ResolveBearer(cfg *config.Config, voice Config) (string, error) {
	if key := strings.TrimSpace(voice.APIKey); key != "" {
		return key, nil
	}
	if key := strings.TrimSpace(os.Getenv("XAI_API_KEY")); key != "" {
		return key, nil
	}
	if cfg == nil || cfg.Providers == nil {
		return "", authErr("not signed in — run `crush login grok`, set XAI_API_KEY, or set a grok provider api_key")
	}
	pc, ok := cfg.Providers.Get(grokProviderID)
	if !ok {
		pc, ok = cfg.Providers.Get(string(catwalk.InferenceProviderGrok))
	}
	if !ok {
		return "", authErr("not signed in — run `crush login grok`, set XAI_API_KEY, or set a grok provider api_key")
	}
	if pc.OAuthToken != nil && strings.TrimSpace(pc.OAuthToken.AccessToken) != "" {
		return pc.OAuthToken.AccessToken, nil
	}
	if key := strings.TrimSpace(pc.APIKey); key != "" {
		return key, nil
	}
	return "", authErr("not signed in — run `crush login grok`, set XAI_API_KEY, or set a grok provider api_key")
}

// ResolveAPIBase returns the STT API base: dedicated voice api_base,
// else the grok provider base_url, else the default.
func ResolveAPIBase(cfg *config.Config, voice Config) string {
	if base := strings.TrimSpace(voice.APIBase); base != "" && base != defaultAPIBase {
		return strings.TrimRight(base, "/")
	}
	if cfg != nil && cfg.Providers != nil {
		if pc, ok := cfg.Providers.Get(grokProviderID); ok {
			if base := strings.TrimSpace(pc.BaseURL); base != "" {
				return strings.TrimRight(base, "/")
			}
		}
	}
	if base := strings.TrimSpace(voice.APIBase); base != "" {
		return strings.TrimRight(base, "/")
	}
	return defaultAPIBase
}
