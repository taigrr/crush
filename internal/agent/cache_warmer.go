package agent

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/schema"
)

const (
	cacheWarmInterval        = 4 * time.Minute
	cacheWarmMaxOutputTokens = int64(1)
)

type cacheWarmer struct {
	model           fantasy.LanguageModel
	messages        []fantasy.Message
	tools           []fantasy.Tool
	providerOptions fantasy.ProviderOptions
	userAgent       string
	warmEvery       time.Duration

	mu       sync.Mutex
	lastWarm time.Time
}

func newCacheWarmer(model Model, messages []fantasy.Message, tools []fantasy.AgentTool, providerOptions fantasy.ProviderOptions) *cacheWarmer {
	if cacheWarmingDisabled() || !cacheWarmingSupportedProvider(model.ModelCfg.Provider) || len(messages) == 0 {
		return nil
	}
	// A thinking budget requires max_tokens > budget_tokens, so a 1-token
	// warm request would be rejected. Rather than bumping max_tokens (and
	// paying for real output), skip warming entirely.
	if model.ModelCfg.Think || hasThinkingBudget(providerOptions) {
		return nil
	}
	return &cacheWarmer{
		model:           model.Model,
		messages:        cloneFantasyMessages(messages),
		tools:           cacheWarmTools(tools),
		providerOptions: providerOptions,
		userAgent:       userAgent,
		warmEvery:       cacheWarmInterval,
	}
}

func cacheWarmingDisabled() bool {
	disabled, _ := strconv.ParseBool(os.Getenv("CRUSH_DISABLE_CACHE_WARMING"))
	return disabled
}

func cacheWarmingSupportedProvider(provider string) bool {
	switch provider {
	case string(catwalk.InferenceProviderAnthropic),
		string(catwalk.InferenceProviderBedrock),
		string(catwalk.InferenceProviderBedrockEurope),
		string(catwalk.InferenceProviderVercel):
		return true
	default:
		return false
	}
}

// hasThinkingBudget reports whether the call enables Anthropic extended
// thinking with an explicit token budget.
func hasThinkingBudget(opts fantasy.ProviderOptions) bool {
	o, ok := opts[anthropic.Name].(*anthropic.ProviderOptions)
	return ok && o != nil && o.Thinking != nil && o.Thinking.BudgetTokens > 0
}

// cacheWarmTools mirrors how fantasy converts AgentTools into request
// tools. The provider cache prefix is hashed over tools, then system, then
// messages, so the warm request must carry an identical tool list or it
// writes a separate cache entry instead of refreshing the live one.
func cacheWarmTools(tools []fantasy.AgentTool) []fantasy.Tool {
	if len(tools) == 0 {
		return nil
	}
	prepared := make([]fantasy.Tool, 0, len(tools))
	for _, tool := range tools {
		info := tool.Info()
		inputSchema := map[string]any{
			"type":       "object",
			"properties": info.Parameters,
			"required":   info.Required,
		}
		schema.Normalize(inputSchema)
		prepared = append(prepared, fantasy.FunctionTool{
			Name:            info.Name,
			Description:     info.Description,
			InputSchema:     inputSchema,
			ProviderOptions: tool.ProviderOptions(),
		})
	}
	return prepared
}

func wrapToolsWithCacheWarmer(tools []fantasy.AgentTool, warmer *cacheWarmer) []fantasy.AgentTool {
	if warmer == nil || len(tools) == 0 {
		return tools
	}
	wrapped := make([]fantasy.AgentTool, len(tools))
	for i, tool := range tools {
		wrapped[i] = &cacheWarmingTool{
			AgentTool: tool,
			warmer:    warmer,
		}
	}
	return wrapped
}

type cacheWarmingTool struct {
	fantasy.AgentTool
	warmer *cacheWarmer
}

func (t *cacheWarmingTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	stop := t.warmer.start(ctx)
	defer stop()
	return t.AgentTool.Run(ctx, call)
}

func (w *cacheWarmer) start(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		timer := time.NewTimer(w.warmEvery)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				w.warm(ctx)
				timer.Reset(w.warmEvery)
			}
		}
	}()
	return cancel
}

func (w *cacheWarmer) warm(ctx context.Context) {
	if !w.reserveWarm(time.Now()) {
		return
	}
	maxOutputTokens := cacheWarmMaxOutputTokens
	// Temperature is intentionally omitted: a fixed value is rejected by
	// Bedrock/Anthropic when thinking or adaptive reasoning is enabled.
	_, err := w.model.Generate(ctx, fantasy.Call{
		Prompt:          append(fantasy.Prompt{}, w.messages...),
		Tools:           w.tools,
		MaxOutputTokens: &maxOutputTokens,
		UserAgent:       w.userAgent,
		ProviderOptions: w.providerOptions,
	})
	if err != nil {
		slog.Debug("Failed to warm provider cache", "error", err)
	}
}

func (w *cacheWarmer) reserveWarm(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.lastWarm.IsZero() && now.Sub(w.lastWarm) < w.warmEvery {
		return false
	}
	w.lastWarm = now
	return true
}
