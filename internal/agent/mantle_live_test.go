package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
	"github.com/taigrr/fantasy/providers/openai"
)

// TestMantleLiveGeneration is an opt-in integration test that hits a real
// Bedrock gateway and generates a response through the exact provider option
// set Crush builds for the "openai"-type bedrock-mantle provider (including
// the HTTP-200 error-envelope transport). It is skipped unless
// CRUSH_MANTLE_LIVE_TEST is set, and requires the same environment a user
// would export:
//
//	CRUSH_MANTLE_LIVE_TEST=1
//	AWS_ENDPOINT_URL_BEDROCK=https://<gateway-host>/gw
//	AWS_BEARER_TOKEN_BEDROCK=nds-<keyid>-<secret>
//	# optional; the model must be a gateway-enabled Bedrock OpenAI model id
//	# (an inference profile, e.g. us.openai.gpt-5.6-sol):
//	CRUSH_MANTLE_TEST_MODEL=us.openai.gpt-5.6-sol
//
// Run it with, e.g.:
//
//	CRUSH_MANTLE_LIVE_TEST=1 go test ./internal/agent -run TestMantleLiveGeneration -v
func TestMantleLiveGeneration(t *testing.T) {
	if os.Getenv("CRUSH_MANTLE_LIVE_TEST") == "" {
		t.Skip("set CRUSH_MANTLE_LIVE_TEST=1 to run the live Bedrock Mantle test")
	}
	gw := strings.TrimSpace(os.Getenv("AWS_ENDPOINT_URL_BEDROCK"))
	token := strings.TrimSpace(os.Getenv("AWS_BEARER_TOKEN_BEDROCK"))
	if gw == "" || token == "" {
		t.Skip("AWS_ENDPOINT_URL_BEDROCK and AWS_BEARER_TOKEN_BEDROCK are required")
	}
	model := os.Getenv("CRUSH_MANTLE_TEST_MODEL")
	if model == "" {
		model = "us.openai.gpt-5.6-sol"
	}

	// Resolve the gateway base URL exactly as config loading does: the
	// OpenAI Responses surface lives at "<gw>/v1/responses", so the provider
	// base is "<gw>/v1" (the SDK appends "/responses").
	base := strings.TrimRight(gw, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}

	// Build the provider with the same options Crush uses for the mantle
	// provider (catalog type "openai"), including the mantle error transport.
	provider, err := openai.New(openaiProviderOptions(base, token, nil, "bedrock-mantle", false)...)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	lm, err := provider.LanguageModel(ctx, model)
	require.NoError(t, err)

	resp, err := lm.Generate(ctx, fantasy.Call{
		Prompt: []fantasy.Message{fantasy.NewUserMessage("Reply with exactly one word: hello")},
	})
	require.NoError(t, err, "mantle generation through the gateway must succeed")
	require.NotEmpty(t, strings.TrimSpace(resp.Content.Text()), "expected non-empty completion text")
	t.Logf("mantle (%s) responded: %q", model, resp.Content.Text())
}
