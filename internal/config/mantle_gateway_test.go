package config

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/catwalk/pkg/catwalk"

	"github.com/taigrr/crush/internal/env"
)

func TestMantleGatewayURL(t *testing.T) {
	tests := []struct {
		name    string
		gw      string
		userPin bool
		want    string
	}{
		{name: "unset", gw: "", userPin: false, want: ""},
		{name: "blank", gw: "   ", userPin: false, want: ""},
		{name: "user pinned wins", gw: "https://gw.example", userPin: true, want: ""},
		{name: "bare origin appends path", gw: "https://gw.example", userPin: false, want: "https://gw.example/openai/v1"},
		{name: "trailing slash trimmed", gw: "https://gw.example/", userPin: false, want: "https://gw.example/openai/v1"},
		{name: "multiple trailing slashes trimmed", gw: "https://gw.example//", userPin: false, want: "https://gw.example/openai/v1"},
		{name: "already has openai path, no double append", gw: "https://gw.example/openai/v1", userPin: false, want: "https://gw.example/openai/v1"},
		{name: "already has openai path with trailing slash", gw: "https://gw.example/openai/v1/", userPin: false, want: "https://gw.example/openai/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, mantleGatewayURL(tt.gw, tt.userPin))
		})
	}
}

func mantleKnownProviders(endpoint string) []catwalk.Provider {
	return []catwalk.Provider{
		{
			ID:          catwalk.InferenceProviderBedrockMantle,
			Type:        catwalk.TypeOpenAI,
			APIEndpoint: endpoint,
			Models:      []catwalk.Model{{ID: "openai.gpt-5.5"}},
		},
	}
}

// The gateway env var must reach prepared.BaseURL through the load path
// when the user has not pinned base_url. This guards against the base URL
// being resolved somewhere unreachable, and against the catalog default
// being mistaken for a user pin.
func TestConfigureProviders_MantleGatewayApplied(t *testing.T) {
	catalogDefault := "https://bedrock-mantle.us-east-2.api.aws/openai/v1"
	cfg := &Config{}
	cfg.setDefaults("/tmp", "")
	env := env.NewFromMap(map[string]string{
		"AWS_BEARER_TOKEN_BEDROCK": "token",
		"AWS_ENDPOINT_URL_BEDROCK": "https://gw.example",
	})
	resolver := NewShellVariableResolver(env)
	require.NoError(t, cfg.configureProviders(testStore(cfg), env, resolver, mantleKnownProviders(catalogDefault)))

	p, ok := cfg.Providers.Get("bedrock-mantle")
	require.True(t, ok, "mantle provider should be present")
	require.Equal(t, "https://gw.example/openai/v1", p.BaseURL)
}

// An explicit base_url pin must win over the gateway env var.
func TestConfigureProviders_MantlePinWins(t *testing.T) {
	catalogDefault := "https://bedrock-mantle.us-east-2.api.aws/openai/v1"
	pinned := "https://pinned.example/openai/v1"
	cfg := &Config{}
	cfg.setDefaults("/tmp", "")
	cfg.Providers.Set("bedrock-mantle", ProviderConfig{BaseURL: pinned})
	env := env.NewFromMap(map[string]string{
		"AWS_BEARER_TOKEN_BEDROCK": "token",
		"AWS_ENDPOINT_URL_BEDROCK": "https://gw.example",
	})
	resolver := NewShellVariableResolver(env)
	require.NoError(t, cfg.configureProviders(testStore(cfg), env, resolver, mantleKnownProviders(catalogDefault)))

	p, ok := cfg.Providers.Get("bedrock-mantle")
	require.True(t, ok, "mantle provider should be present")
	require.Equal(t, pinned, p.BaseURL)
}

// Without the gateway env var, the catalog default is kept.
func TestConfigureProviders_MantleNoGateway(t *testing.T) {
	catalogDefault := "https://bedrock-mantle.us-east-2.api.aws/openai/v1"
	cfg := &Config{}
	cfg.setDefaults("/tmp", "")
	env := env.NewFromMap(map[string]string{
		"AWS_BEARER_TOKEN_BEDROCK": "token",
	})
	resolver := NewShellVariableResolver(env)
	require.NoError(t, cfg.configureProviders(testStore(cfg), env, resolver, mantleKnownProviders(catalogDefault)))

	p, ok := cfg.Providers.Get("bedrock-mantle")
	require.True(t, ok, "mantle provider should be present")
	require.Equal(t, catalogDefault, p.BaseURL)
}
