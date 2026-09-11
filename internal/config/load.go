package config

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	powernapConfig "github.com/charmbracelet/x/powernap/pkg/config"
	"github.com/qjebbs/go-jsons"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/hyper"
	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/env"
	"github.com/taigrr/crush/internal/filepathext"
	"github.com/taigrr/crush/internal/fsext"
	"github.com/taigrr/crush/internal/home"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Load loads the configuration from the default paths and returns a
// ConfigStore that owns both the pure-data Config and all runtime state.
func Load(workingDir, dataDir string, debug bool) (*ConfigStore, error) {
	// Migrate deprecated disable_notifications before loading config.
	migrateDisableNotifications()

	// Drop a fresh schema.json next to the global config so editors can
	// validate fork-specific fields without hitting a stale upstream URL.
	go writeGlobalSchema()

	configPaths := lookupConfigs(workingDir)

	cfg, loadedPaths, err := loadFromConfigPaths(configPaths)
	if err != nil {
		return nil, fmt.Errorf("failed to load config from paths %v: %w", configPaths, err)
	}

	cfg.setDefaults(workingDir, dataDir)

	store := &ConfigStore{
		config:         cfg,
		workingDir:     workingDir,
		globalDataPath: GlobalConfigData(),
		workspacePath:  filepath.Join(cfg.Options.DataDirectory, fmt.Sprintf("%s.json", appName)),
		loadedPaths:    loadedPaths,
	}

	if debug {
		cfg.Options.Debug = true
	}

	// Load workspace config last so it has highest priority.
	if wsData, err := os.ReadFile(store.workspacePath); err == nil && len(wsData) > 0 {
		if !json.Valid(wsData) {
			return nil, fmt.Errorf("invalid JSON in config file %s", store.workspacePath)
		}
		// Embedding is a global-only setting: snapshot the value from the
		// global layer so a workspace config can never fragment the
		// embedding space (see docs/specs/EMBEDDINGS_AND_VECTOR_SEARCH.md
		// §3.2).
		globalEmbedding := cfg.Embedding
		merged, mergeErr := loadFromBytes(append([][]byte{mustMarshalConfig(cfg)}, wsData))
		if mergeErr == nil {
			// Preserve defaults that setDefaults already applied.
			dataDir := cfg.Options.DataDirectory
			*cfg = *merged
			cfg.setDefaults(workingDir, dataDir)
			if cfg.Embedding.Signature() != globalEmbedding.Signature() {
				slog.Warn("Ignoring 'embedding' from workspace config; it is a global-only setting",
					"path", store.workspacePath)
			}
			cfg.Embedding = globalEmbedding
			store.config = cfg
			store.loadedPaths = append(store.loadedPaths, store.workspacePath)
		}
	}

	// Validate hooks after all config merging is complete so workspace
	// hooks also get their matcher regexes compiled.
	if err := cfg.ValidateHooks(); err != nil {
		return nil, fmt.Errorf("invalid hook configuration: %w", err)
	}

	if !isInsideWorktree() {
		const depth = 2
		const items = 100
		slog.Warn("No git repository detected in working directory, will limit file walk operations", "depth", depth, "items", items)
		assignIfNil(&cfg.Tools.Ls.MaxDepth, depth)
		assignIfNil(&cfg.Tools.Ls.MaxItems, items)
		assignIfNil(&cfg.Options.TUI.Completions.MaxDepth, depth)
		assignIfNil(&cfg.Options.TUI.Completions.MaxItems, items)
	}

	if isAppleTerminal() {
		slog.Warn("Detected Apple Terminal, enabling transparent mode")
		assignIfNil(&cfg.Options.TUI.Transparent, true)
	}

	// Load known providers, this loads the config from catwalk
	providers, err := Providers(cfg)
	if err != nil {
		return nil, err
	}
	store.knownProviders = providers

	env := env.New()
	// Configure providers
	valueResolver := NewShellVariableResolver(env)
	store.resolver = valueResolver

	// Hold reloadMu during initial load to prevent configureProviders
	// from triggering auto-reload via RemoveConfigField.
	store.reloadMu.Lock()
	defer store.reloadMu.Unlock()

	if err := cfg.configureProviders(store, env, valueResolver, store.knownProviders); err != nil {
		return nil, fmt.Errorf("failed to configure providers: %w", err)
	}

	if !cfg.IsConfigured() {
		slog.Warn("No providers configured")
		// Still capture the staleness snapshot so reload-on-reconnect
		// works even when no providers are configured.
		store.captureStalenessSnapshot(loadedPaths)
		return store, nil
	}

	if err := configureSelectedModels(store, store.knownProviders, true); err != nil {
		return nil, fmt.Errorf("failed to configure selected models: %w", err)
	}
	store.SetupAgents()

	// Capture initial staleness snapshot
	store.captureStalenessSnapshot(loadedPaths)

	return store, nil
}

// mustMarshalConfig marshals the config to JSON bytes, returning empty JSON on
// error.
func mustMarshalConfig(cfg *Config) []byte {
	data, err := json.Marshal(cfg)
	if err != nil {
		return []byte("{}")
	}
	return data
}

func PushPopCrushEnv() func() {
	var found []string
	for _, ev := range os.Environ() {
		if strings.HasPrefix(ev, "CRUSH_") {
			pair := strings.SplitN(ev, "=", 2)
			if len(pair) != 2 {
				continue
			}
			found = append(found, strings.TrimPrefix(pair[0], "CRUSH_"))
		}
	}
	backups := make(map[string]string)
	for _, ev := range found {
		backups[ev] = os.Getenv(ev)
	}

	for _, ev := range found {
		os.Setenv(ev, os.Getenv("CRUSH_"+ev))
	}

	restore := func() {
		for k, v := range backups {
			os.Setenv(k, v)
		}
	}
	return restore
}

// mergeProviderModels merges user-configured models with the provider's
// built-in models. User models take precedence and come first; built-in
// models that share an ID with a user model are dropped. Within each set the
// first occurrence of an ID wins, so duplicate IDs are removed. A model with
// an empty Name defaults its Name to its ID.
func mergeProviderModels(configModels, providerModels []catwalk.Model) []catwalk.Model {
	models := []catwalk.Model{}
	seen := make(map[string]struct{})
	for _, set := range [][]catwalk.Model{configModels, providerModels} {
		for _, model := range set {
			if _, ok := seen[model.ID]; ok {
				continue
			}
			seen[model.ID] = struct{}{}
			if model.Name == "" {
				model.Name = model.ID
			}
			models = append(models, model)
		}
	}
	return models
}

// resolveProviderHeaders merges default and extra headers, resolves any
// $(...) / $VAR templates in the values, and drops headers that resolve to
// the empty string. Extra headers override default headers with the same key.
// A failing resolution aborts with an error, matching the MCP header
// contract.
func resolveProviderHeaders(resolver VariableResolver, providerID string, defaultHeaders, extraHeaders map[string]string) (map[string]string, error) {
	headers := map[string]string{}
	if len(defaultHeaders) > 0 {
		maps.Copy(headers, defaultHeaders)
	}
	if len(extraHeaders) > 0 {
		maps.Copy(headers, extraHeaders)
	}
	for k, v := range headers {
		resolved, err := resolver.ResolveValue(v)
		if err != nil {
			return nil, fmt.Errorf("resolving provider %s header %q: %w", providerID, k, err)
		}
		if resolved == "" {
			delete(headers, k)
			continue
		}
		headers[k] = resolved
	}
	return headers, nil
}

func (c *Config) configureProviders(store *ConfigStore, env env.Env, resolver VariableResolver, knownProviders []catwalk.Provider) error {
	knownProviderNames := make(map[string]struct{})
	restore := PushPopCrushEnv()
	defer restore()

	// When disable_default_providers is enabled, skip all default/embedded
	// providers entirely. Users must fully specify any providers they want.
	// We skip to the custom provider validation loop which handles all
	// user-configured providers uniformly.
	if c.Options.DisableDefaultProviders {
		knownProviders = nil
	}

	for _, p := range knownProviders {
		knownProviderNames[string(p.ID)] = struct{}{}
		config, configExists := c.Providers.Get(string(p.ID))
		// if the user configured a known provider we need to allow it to override a couple of parameters
		if configExists {
			if config.BaseURL != "" {
				p.APIEndpoint = config.BaseURL
			}
			if config.APIKey != "" {
				p.APIKey = config.APIKey
			}
			if len(config.Models) > 0 {
				p.Models = mergeProviderModels(config.Models, p.Models)
			}
		}

		// Provider headers use the same error contract as MCP headers:
		// a failing $(...) aborts the provider load with a clear
		// message, and a header that resolves to the empty string
		// (unset bare $VAR under lenient nounset, $(echo), or literal
		// "") is dropped from the outgoing request.
		headers, err := resolveProviderHeaders(resolver, string(p.ID), p.DefaultHeaders, config.ExtraHeaders)
		if err != nil {
			return err
		}
		prepared := ProviderConfig{
			ID:                 string(p.ID),
			Name:               p.Name,
			BaseURL:            p.APIEndpoint,
			APIKey:             p.APIKey,
			APIKeyTemplate:     p.APIKey, // Store original template for re-resolution
			OAuthToken:         config.OAuthToken,
			Type:               p.Type,
			Disable:            config.Disable,
			SystemPromptPrefix: config.SystemPromptPrefix,
			ExtraHeaders:       headers,
			ExtraBody:          config.ExtraBody,
			ExtraParams:        make(map[string]string),
			Models:             p.Models,
		}

		if c.applyProviderSpecificConfig(store, env, resolver, p, config, configExists, &prepared) {
			continue
		}
		c.Providers.Set(string(p.ID), prepared)
	}

	// validate the custom providers
	for id, providerConfig := range c.Providers.Seq2() {
		if _, ok := knownProviderNames[id]; ok {
			continue
		}

		// Make sure the provider ID is set
		providerConfig.ID = id
		providerConfig.Name = cmp.Or(providerConfig.Name, id) // Use ID as name if not set
		// default to OpenAI if not set
		providerConfig.Type = cmp.Or(providerConfig.Type, catwalk.TypeOpenAICompat)
		if !slices.Contains(catwalk.KnownProviderTypes(), providerConfig.Type) && providerConfig.Type != hyper.Name {
			slog.Warn("Skipping custom provider due to unsupported provider type", "provider", id)
			c.Providers.Del(id)
			continue
		}

		if providerConfig.Disable {
			slog.Debug("Skipping custom provider due to disable flag", "provider", id)
			c.Providers.Del(id)
			continue
		}
		if providerConfig.APIKey == "" {
			slog.Warn("Provider is missing API key, this might be OK for local providers", "provider", id)
		}
		if providerConfig.BaseURL == "" {
			slog.Warn("Skipping custom provider due to missing API endpoint", "provider", id)
			c.Providers.Del(id)
			continue
		}
		if len(providerConfig.Models) == 0 {
			slog.Warn("Skipping custom provider because the provider has no models", "provider", id)
			c.Providers.Del(id)
			continue
		}
		apiKey, err := resolver.ResolveValue(providerConfig.APIKey)
		if apiKey == "" || err != nil {
			slog.Warn("Provider is missing API key, this might be OK for local providers", "provider", id)
		}
		baseURL, err := resolver.ResolveValue(providerConfig.BaseURL)
		if baseURL == "" || err != nil {
			slog.Warn("Skipping custom provider due to missing API endpoint", "provider", id, "error", err)
			c.Providers.Del(id)
			continue
		}

		// Custom-provider headers share the MCP error contract; see
		// the known-provider loop above.
		if len(providerConfig.ExtraHeaders) > 0 {
			resolvedHeaders, err := resolveProviderHeaders(resolver, id, nil, providerConfig.ExtraHeaders)
			if err != nil {
				return err
			}
			providerConfig.ExtraHeaders = resolvedHeaders
		}

		c.Providers.Set(id, providerConfig)
	}

	if c.Providers.Len() == 0 && c.Options.DisableDefaultProviders {
		return fmt.Errorf("default providers are disabled and there are no custom providers are configured")
	}

	return nil
}

// applyProviderSpecificConfig applies per-provider quirks (credentials,
// endpoints, extra params) to a known provider's prepared config. It returns
// true when the provider should be skipped (and removes it from c.Providers
// when it was user-configured). prepared is mutated in place.
func (c *Config) applyProviderSpecificConfig(store *ConfigStore, env env.Env, resolver VariableResolver, p catwalk.Provider, config ProviderConfig, configExists bool, prepared *ProviderConfig) (skip bool) {
	// skipMissing removes a user-configured provider and logs why; bare
	// (default) providers are silently dropped.
	skipMissing := func(msg string, args ...any) bool {
		if configExists {
			slog.Warn(msg, args...)
			c.Providers.Del(string(p.ID))
		}
		return true
	}

	switch {
	case p.ID == catwalk.InferenceProviderAnthropic && config.OAuthToken != nil:
		// Claude Code subscription is not supported anymore. Remove to show onboarding.
		// RemoveConfigField persists the deletion to disk. The in-memory
		// state is kept consistent by the Providers.Del call below; any
		// concurrent reload that races with this write will also see the
		// removal because it re-reads from disk.
		store.RemoveConfigField(ScopeGlobal, "providers.anthropic")
		c.Providers.Del(string(p.ID))
		return true
	case p.ID == catwalk.InferenceProviderCopilot && config.OAuthToken != nil:
		prepared.SetupGitHubCopilot()
	case p.ID == catwalk.InferenceProviderGrok && config.OAuthToken != nil:
		prepared.SetupGrok()
	}

	switch p.ID {
	// Handle specific providers that require additional configuration
	case catwalk.InferenceProviderVertexAI:
		var (
			project  = env.Get("VERTEXAI_PROJECT")
			location = env.Get("VERTEXAI_LOCATION")
		)
		if project == "" || location == "" {
			return skipMissing("Skipping Vertex AI provider due to missing credentials")
		}
		prepared.ExtraParams["project"] = project
		prepared.ExtraParams["location"] = location
	case catwalk.InferenceProviderAzure:
		endpoint, err := resolver.ResolveValue(p.APIEndpoint)
		if err != nil || endpoint == "" {
			return skipMissing("Skipping Azure provider due to missing API endpoint", "provider", p.ID, "error", err)
		}
		prepared.BaseURL = endpoint
		prepared.ExtraParams["apiVersion"] = env.Get("AZURE_OPENAI_API_VERSION")
	case catwalk.InferenceProviderBedrock, catwalk.InferenceProviderBedrockEurope:
		if p.APIKey == "" && !hasAWSCredentials(env) {
			return skipMissing("Skipping Bedrock provider due to missing AWS credentials")
		}
	case catwalk.InferenceProviderBedrockMantle:
		// The Bedrock mantle (OpenAI-compatible) endpoint authenticates
		// with a Bedrock API key via the Authorization header, not SigV4,
		// so it requires AWS_BEARER_TOKEN_BEDROCK.
		if prepared.APIKey == "" {
			prepared.APIKey = env.Get("AWS_BEARER_TOKEN_BEDROCK")
			prepared.APIKeyTemplate = prepared.APIKey
		}
		if prepared.APIKey == "" {
			return skipMissing("Skipping Bedrock Mantle provider due to missing AWS_BEARER_TOKEN_BEDROCK")
		}
		// A corporate gateway fronting Bedrock (AWS_ENDPOINT_URL_BEDROCK,
		// the same variable the native Bedrock provider consumes) can also
		// front Mantle's OpenAI-compatible surface. Route to it unless the
		// user pinned providers.bedrock-mantle.base_url, in which case the
		// explicit pin wins. The raw user config.BaseURL is the true pin
		// signal; prepared.BaseURL is always the catalog default here.
		userPinnedMantle := configExists && config.BaseURL != ""
		if gw := mantleGatewayURL(env.Get("AWS_ENDPOINT_URL_BEDROCK"), userPinnedMantle); gw != "" {
			prepared.BaseURL = gw
		}
	case catwalk.InferenceProvider("hyper"):
		if apiKey := env.Get("HYPER_API_KEY"); apiKey != "" {
			prepared.APIKey = apiKey
			prepared.APIKeyTemplate = apiKey
		} else {
			v, err := resolver.ResolveValue(p.APIKey)
			if v == "" || err != nil {
				return skipMissing("Skipping Hyper provider due to missing API key", "provider", p.ID)
			}
		}
	default:
		// if the provider api or endpoint are missing we skip them
		v, err := resolver.ResolveValue(p.APIKey)
		if v == "" || err != nil {
			return skipMissing("Skipping provider due to missing API key", "provider", p.ID)
		}
	}
	return false
}

// mantleGatewayURL returns the OpenAI-compatible base URL Bedrock Mantle
// should use when a corporate gateway fronts Bedrock, or "" to leave the
// configured base URL alone. gw is the value of AWS_ENDPOINT_URL_BEDROCK;
// it is ignored (returns "") when blank or when the user pinned
// providers.bedrock-mantle.base_url themselves.
//
// The gateway value is treated as an origin and the OpenAI base path "/v1"
// is appended (the provider then posts to "<base>/responses"). The gateway
// exposes the OpenAI Responses surface at "<gw>/v1/responses" — mapping it
// to Bedrock's native "/openai/v1/responses" — so a bare origin, a "/v1"
// suffix, and a direct-Bedrock "/openai/v1" suffix all resolve to a valid
// base and are left idempotent.
func mantleGatewayURL(gw string, userPinned bool) string {
	gw = strings.TrimSpace(gw)
	if gw == "" || userPinned {
		return ""
	}
	gw = strings.TrimRight(gw, "/")
	if strings.HasSuffix(gw, "/v1") {
		return gw
	}
	return gw + "/v1"
}

func (c *Config) setDefaults(workingDir, dataDir string) {
	if c.Options == nil {
		c.Options = &Options{}
	}
	if c.Options.TUI == nil {
		c.Options.TUI = &TUIOptions{}
	}
	if dataDir != "" {
		c.Options.DataDirectory = dataDir
	} else if c.Options.DataDirectory == "" {
		if path, ok := fsext.LookupClosestBounded(workingDir, projectBoundary(workingDir), defaultDataDirectory); ok {
			c.Options.DataDirectory = path
		} else {
			c.Options.DataDirectory = filepath.Join(workingDir, defaultDataDirectory)
		}
	}
	c.Options.DataDirectory = filepath.Clean(filepathext.SmartJoin(workingDir, c.Options.DataDirectory))
	if c.Providers == nil {
		c.Providers = csync.NewMap[string, ProviderConfig]()
	}
	if c.Models == nil {
		c.Models = make(map[SelectedModelType]SelectedModel)
	}
	if c.RecentModels == nil {
		c.RecentModels = make(map[SelectedModelType][]SelectedModel)
	}
	if c.MCP == nil {
		c.MCP = make(map[string]MCPConfig)
	}
	// Drop orphaned OAuth token entries left behind when a user removes
	// an MCP from crush.json. See MCPConfig.isOrphanedToken.
	for name, m := range c.MCP {
		if m.isOrphanedToken() {
			delete(c.MCP, name)
		}
	}
	if c.LSP == nil {
		c.LSP = make(map[string]LSPConfig)
	}

	// Set default snapshot exclusions if not configured.
	if c.Snapshots == nil {
		c.Snapshots = &SnapshotsConfig{}
	}
	if len(c.Snapshots.Exclude) == 0 {
		c.Snapshots.Exclude = defaultSnapshotExclusions()
	}

	// Set default worktree hooks if not configured.
	if c.Worktree == nil {
		c.Worktree = &WorktreeConfig{}
	}
	if len(c.Worktree.PostCreate) == 0 {
		c.Worktree.PostCreate = defaultPostCreateHooks()
	}

	// Apply defaults to LSP configurations
	c.applyLSPDefaults()

	// Add the default context paths if they are not already present
	c.Options.ContextPaths = append(slices.Clone(defaultContextPaths), c.Options.ContextPaths...)
	slices.Sort(c.Options.ContextPaths)
	c.Options.ContextPaths = slices.Compact(c.Options.ContextPaths)

	// Add the default skills directories if not already present.
	for _, dir := range GlobalSkillsDirs() {
		if !slices.Contains(c.Options.SkillsPaths, dir) {
			c.Options.SkillsPaths = append(c.Options.SkillsPaths, dir)
		}
	}

	// Project specific skills dirs.
	c.Options.SkillsPaths = append(c.Options.SkillsPaths, ProjectSkillsDir(workingDir)...)

	if str, ok := os.LookupEnv("CRUSH_DISABLE_PROVIDER_AUTO_UPDATE"); ok {
		c.Options.DisableProviderAutoUpdate, _ = strconv.ParseBool(str)
	}

	if str, ok := os.LookupEnv("CRUSH_DISABLE_DEFAULT_PROVIDERS"); ok {
		c.Options.DisableDefaultProviders, _ = strconv.ParseBool(str)
	}

	// CRUSH_LOW_BANDWIDTH forces reduced-motion mode on regardless of
	// the persisted config. Useful when SSH'd into a slow link that
	// the user wouldn't want to persist back to disk.
	if str, ok := os.LookupEnv("CRUSH_LOW_BANDWIDTH"); ok {
		if v, err := strconv.ParseBool(str); err == nil {
			c.Options.TUI.LowBandwidth = &v
		}
	}

	if c.Options.Attribution == nil {
		c.Options.Attribution = &Attribution{
			TrailerStyle: TrailerStyleAssistedBy,
		}
	} else if c.Options.Attribution.TrailerStyle == "" {
		// Migrate deprecated co_authored_by or apply default
		if c.Options.Attribution.CoAuthoredBy != nil {
			if *c.Options.Attribution.CoAuthoredBy {
				c.Options.Attribution.TrailerStyle = TrailerStyleCoAuthoredBy
			} else {
				c.Options.Attribution.TrailerStyle = TrailerStyleNone
			}
		} else {
			c.Options.Attribution.TrailerStyle = TrailerStyleAssistedBy
		}
	}

	c.Options.InitializeAs = cmp.Or(c.Options.InitializeAs, defaultInitializeAs)
}

// applyLSPDefaults applies default values from powernap to LSP configurations
func (c *Config) applyLSPDefaults() {
	// Get powernap's default configuration
	configManager := powernapConfig.NewManager()
	configManager.LoadDefaults()

	// Apply defaults to each LSP configuration
	for name, cfg := range c.LSP {
		// Try to get defaults from powernap based on name or command name.
		base, ok := configManager.GetServer(name)
		if !ok {
			base, ok = configManager.GetServer(cfg.Command)
			if !ok {
				continue
			}
		}
		if cfg.Options == nil {
			cfg.Options = base.Settings
		}
		if cfg.InitOptions == nil {
			cfg.InitOptions = base.InitOptions
		}
		if len(cfg.FileTypes) == 0 {
			cfg.FileTypes = base.FileTypes
		}
		if len(cfg.RootMarkers) == 0 {
			cfg.RootMarkers = base.RootMarkers
		}
		cfg.Command = cmp.Or(cfg.Command, base.Command)
		if len(cfg.Args) == 0 {
			cfg.Args = base.Args
		}
		if len(cfg.Env) == 0 {
			cfg.Env = base.Environment
		}
		// Update the config in the map
		c.LSP[name] = cfg
	}
}

func (c *Config) defaultModelSelection(knownProviders []catwalk.Provider) (largeModel SelectedModel, smallModel SelectedModel, err error) {
	if len(knownProviders) == 0 && c.Providers.Len() == 0 {
		err = fmt.Errorf("no providers configured, please configure at least one provider")
		return largeModel, smallModel, err
	}

	// Use the first provider enabled based on the known providers order
	// if no provider found that is known use the first provider configured
	for _, p := range knownProviders {
		providerConfig, ok := c.Providers.Get(string(p.ID))
		if !ok || providerConfig.Disable {
			continue
		}
		defaultLargeModel := c.GetModel(string(p.ID), p.DefaultLargeModelID)
		if defaultLargeModel == nil {
			slog.Warn("Default large model %s not found for provider %s", p.DefaultLargeModelID, p.ID)
			if len(providerConfig.Models) == 0 {
				return largeModel, smallModel, fmt.Errorf("default large model %s not found for provider %s", p.DefaultLargeModelID, p.ID)
			}
			defaultLargeModel = &providerConfig.Models[0]
		}
		largeModel = SelectedModel{
			Provider:        string(p.ID),
			Model:           defaultLargeModel.ID,
			MaxTokens:       defaultLargeModel.DefaultMaxTokens,
			ReasoningEffort: defaultLargeModel.DefaultReasoningEffort,
		}

		defaultSmallModel := c.GetModel(string(p.ID), p.DefaultSmallModelID)
		if defaultSmallModel == nil {
			slog.Warn("Default small model %s not found for provider %s", p.DefaultSmallModelID, p.ID)
			if len(providerConfig.Models) == 0 {
				return largeModel, smallModel, fmt.Errorf("default small model %s not found for provider %s", p.DefaultSmallModelID, p.ID)
			}
			defaultSmallModel = &providerConfig.Models[0]
		}
		smallModel = SelectedModel{
			Provider:        string(p.ID),
			Model:           defaultSmallModel.ID,
			MaxTokens:       defaultSmallModel.DefaultMaxTokens,
			ReasoningEffort: defaultSmallModel.DefaultReasoningEffort,
		}
		return largeModel, smallModel, err
	}

	enabledProviders := c.EnabledProviders()
	slices.SortFunc(enabledProviders, func(a, b ProviderConfig) int {
		return strings.Compare(a.ID, b.ID)
	})

	if len(enabledProviders) == 0 {
		err = fmt.Errorf("no providers configured, please configure at least one provider")
		return largeModel, smallModel, err
	}

	providerConfig := enabledProviders[0]
	if len(providerConfig.Models) == 0 {
		err = fmt.Errorf("provider %s has no models configured", providerConfig.ID)
		return largeModel, smallModel, err
	}
	defaultLargeModel := c.GetModel(providerConfig.ID, providerConfig.Models[0].ID)
	largeModel = SelectedModel{
		Provider:  providerConfig.ID,
		Model:     defaultLargeModel.ID,
		MaxTokens: defaultLargeModel.DefaultMaxTokens,
	}
	defaultSmallModel := c.GetModel(providerConfig.ID, providerConfig.Models[0].ID)
	smallModel = SelectedModel{
		Provider:  providerConfig.ID,
		Model:     defaultSmallModel.ID,
		MaxTokens: defaultSmallModel.DefaultMaxTokens,
	}
	return largeModel, smallModel, err
}

// applyModelOverrides copies user-configured override fields from override
// onto target, falling back to the resolved catwalk model's defaults where the
// override is unset. target.Provider and target.Model are assumed to already
// be resolved. Think is copied unconditionally because it is a plain bool with
// no "unset" sentinel: a user who toggles it off must be able to override an
// on-by-default value.
func applyModelOverrides(target *SelectedModel, override SelectedModel, model *catwalk.Model) {
	if override.MaxTokens > 0 {
		target.MaxTokens = override.MaxTokens
	} else {
		target.MaxTokens = model.DefaultMaxTokens
	}
	if override.ReasoningEffort != "" {
		target.ReasoningEffort = override.ReasoningEffort
	}
	target.Think = override.Think
	if override.Temperature != nil {
		target.Temperature = override.Temperature
	}
	if override.TopP != nil {
		target.TopP = override.TopP
	}
	if override.TopK != nil {
		target.TopK = override.TopK
	}
	if override.FrequencyPenalty != nil {
		target.FrequencyPenalty = override.FrequencyPenalty
	}
	if override.PresencePenalty != nil {
		target.PresencePenalty = override.PresencePenalty
	}
	if override.ProviderOptions != nil {
		target.ProviderOptions = override.ProviderOptions
	}
}

func configureSelectedModels(store *ConfigStore, knownProviders []catwalk.Provider, persist bool) error {
	c := store.config
	defaultLarge, defaultSmall, err := c.defaultModelSelection(knownProviders)
	if err != nil {
		return fmt.Errorf("failed to select default models: %w", err)
	}
	large, small := defaultLarge, defaultSmall

	largeModelSelected, largeModelConfigured := c.Models[SelectedModelTypeLarge]
	if largeModelConfigured {
		if largeModelSelected.Model != "" {
			large.Model = largeModelSelected.Model
		}
		if largeModelSelected.Provider != "" {
			large.Provider = largeModelSelected.Provider
		}
		model := c.GetModel(large.Provider, large.Model)
		if model == nil {
			large = defaultLarge
			if persist {
				if err := store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeLarge, large); err != nil {
					return fmt.Errorf("failed to update preferred large model: %w", err)
				}
			}
		} else {
			applyModelOverrides(&large, largeModelSelected, model)
		}
	}
	smallModelSelected, smallModelConfigured := c.Models[SelectedModelTypeSmall]
	if smallModelConfigured {
		if smallModelSelected.Model != "" {
			small.Model = smallModelSelected.Model
		}
		if smallModelSelected.Provider != "" {
			small.Provider = smallModelSelected.Provider
		}

		model := c.GetModel(small.Provider, small.Model)
		if model == nil {
			small = defaultSmall
			if persist {
				if err := store.UpdatePreferredModel(ScopeGlobal, SelectedModelTypeSmall, small); err != nil {
					return fmt.Errorf("failed to update preferred small model: %w", err)
				}
			}
		} else {
			applyModelOverrides(&small, smallModelSelected, model)
		}
	}

	// When small isn't explicitly configured and the provider isn't a
	// known built-in, use the large model as the small model. This
	// prevents two different models from being requested concurrently
	// for local/openai-compat providers.
	if !smallModelConfigured {
		isKnownProvider := false
		for _, kp := range knownProviders {
			if string(kp.ID) == small.Provider {
				isKnownProvider = true
				break
			}
		}
		if !isKnownProvider {
			slog.Warn("Using large model as small model for unknown provider", "provider", large.Provider, "model", large.Model)
			small = large
		}
	}

	c.Models[SelectedModelTypeLarge] = large
	c.Models[SelectedModelTypeSmall] = small

	// The worker slot and user-defined roles are optional and have
	// no default. Unlike large/small we never substitute a fallback for
	// an unresolvable selection: we drop it with a warning, so a typo
	// degrades to "that role is unavailable" rather than silently
	// running an arbitrary model under a name the user chose on purpose.
	for typ, sel := range c.Models {
		if typ == SelectedModelTypeLarge || typ == SelectedModelTypeSmall {
			continue
		}
		if sel.Model == "" || sel.Provider == "" {
			slog.Warn("Model role is incomplete; ignoring", "role", typ)
			delete(c.Models, typ)
			continue
		}
		model := c.EnabledModel(sel.Provider, sel.Model)
		if model == nil {
			slog.Warn("Model role points at a model no configured provider offers; ignoring",
				"role", typ, "provider", sel.Provider, "model", sel.Model)
			delete(c.Models, typ)
			continue
		}
		resolved := sel
		applyModelOverrides(&resolved, sel, model)
		c.Models[typ] = resolved
	}
	return nil
}

// lookupConfigs searches config files starting at cwd and walking up
// through the current project. The upward walk stops at the git
// working tree root when one can be detected, otherwise at cwd itself,
// so an unrelated crush.json placed above the project is never picked
// up. Global user-level config locations are always included
// regardless of the boundary.
func lookupConfigs(cwd string) []string {
	// prepend default config paths
	configPaths := []string{
		GlobalConfig(),
		GlobalConfigData(),
	}
	configPaths = append(configPaths, scopedIncludes(GlobalConfig(), cwd)...)

	configNames := []string{appName + ".json", "." + appName + ".json"}

	foundConfigs, err := fsext.LookupBounded(cwd, projectBoundary(cwd), configNames...)
	if err != nil {
		// returns at least default configs
		return configPaths
	}

	// reverse order so last config has more priority
	slices.Reverse(foundConfigs)

	return append(configPaths, foundConfigs...)
}

// scopedIncludes returns the include files declared in the global config
// at globalPath whose dir contains cwd, in declaration order. Only the
// global config is consulted: includes declared in an included file, a
// project config, or a workspace config are ignored, so a checked-out
// repository can never widen its own config search. Unreadable or
// malformed input yields no includes; the full config load reports the
// JSON error with the right file name.
func scopedIncludes(globalPath, cwd string) []string {
	data, err := os.ReadFile(globalPath)
	if err != nil || len(data) == 0 {
		return nil
	}
	var global struct {
		Includes []ConfigInclude `json:"includes"`
	}
	if err := json.Unmarshal(data, &global); err != nil {
		return nil
	}
	if len(global.Includes) == 0 {
		return nil
	}

	cwd = canonicalDir(cwd)
	var paths []string
	for _, inc := range global.Includes {
		if inc.Dir == "" || inc.Path == "" {
			continue
		}
		dir := canonicalDir(os.ExpandEnv(home.Long(inc.Dir)))
		if !isWithin(dir, cwd) {
			continue
		}
		path := os.ExpandEnv(home.Long(inc.Path))
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(globalPath), path)
		}
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	return paths
}

// canonicalDir returns dir as an absolute path with symlinks resolved
// when possible, so a macOS /var vs /private/var mismatch or a symlinked
// code directory still compares equal.
func canonicalDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// isWithin reports whether child is parent itself or a descendant of it.
func isWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func loadFromConfigPaths(configPaths []string) (*Config, []string, error) {
	var configs [][]byte
	var loaded []string

	for _, path := range configPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("failed to open config file %s: %w", path, err)
		}
		if len(data) == 0 {
			continue
		}
		if !json.Valid(data) {
			return nil, nil, fmt.Errorf("invalid JSON in config file %s", path)
		}
		configs = append(configs, data)
		loaded = append(loaded, path)
	}

	cfg, err := loadFromBytes(configs)
	if err != nil {
		return nil, nil, err
	}
	return cfg, loaded, nil
}

func loadFromBytes(configs [][]byte) (*Config, error) {
	if len(configs) == 0 {
		return &Config{}, nil
	}

	data, err := jsons.Merge(configs)
	if err != nil {
		return nil, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func hasAWSCredentials(env env.Env) bool {
	if env.Get("AWS_BEARER_TOKEN_BEDROCK") != "" {
		return true
	}

	if env.Get("AWS_ACCESS_KEY_ID") != "" && env.Get("AWS_SECRET_ACCESS_KEY") != "" {
		return true
	}

	// Any single one of these env vars is enough to imply that AWS
	// credentials are resolvable (via a profile, the default region's
	// instance/role credentials, or container credential endpoints).
	anyOf := []string{
		"AWS_PROFILE",
		"AWS_DEFAULT_PROFILE",
		"AWS_REGION",
		"AWS_DEFAULT_REGION",
		"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI",
	}
	for _, key := range anyOf {
		if env.Get(key) != "" {
			return true
		}
	}

	if _, err := os.Stat(filepath.Join(home.Dir(), ".aws/credentials")); err == nil && !testing.Testing() {
		return true
	}
	if _, err := os.Stat(filepath.Join(home.Dir(), ".aws/login")); err == nil && !testing.Testing() {
		return true
	}

	return false
}

// migrateDisableNotifications migrates the deprecated disable_notifications
// field to notification_style. It checks both the user config (~/.config) and
// data config (~/.local) files. If disable_notifications is true, it sets
// notification_style to "disabled" in the data file. Regardless of value, it
// removes disable_notifications from any file that contains it.
func migrateDisableNotifications() {
	globalConfig := GlobalConfig()
	dataConfig := GlobalConfigData()

	var wasDisabled bool
	filesToClean := []string{}

	for _, path := range []string{globalConfig, dataConfig} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if gjson.Get(string(data), "options.disable_notifications").Exists() {
			filesToClean = append(filesToClean, path)
			if gjson.Get(string(data), "options.disable_notifications").Bool() {
				wasDisabled = true
			}
		}
	}

	if len(filesToClean) == 0 {
		return
	}

	// If notifications were disabled, persist the equivalent notification_style.
	if wasDisabled {
		data, err := os.ReadFile(dataConfig)
		if err == nil {
			if !gjson.Get(string(data), "options.notification_style").Exists() {
				updated, err := sjson.Set(string(data), "options.notification_style", "disabled")
				if err == nil {
					if err := atomicWriteFile(dataConfig, []byte(updated), 0o600); err != nil {
						slog.Warn("Failed to migrate disable_notifications to notification_style", "error", err)
					} else {
						slog.Info("Migrated disable_notifications: true to notification_style: disabled")
					}
				}
			}
		}
	}

	// Remove disable_notifications from all files that contain it.
	for _, path := range filesToClean {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		updated, err := sjson.Delete(string(data), "options.disable_notifications")
		if err != nil {
			slog.Warn("Failed to remove deprecated disable_notifications field", "path", path, "error", err)
			continue
		}
		if err := atomicWriteFile(path, []byte(updated), 0o600); err != nil {
			slog.Warn("Failed to write migrated config", "path", path, "error", err)
		}
	}
}

// GlobalConfig returns the global configuration file path for the application.
func GlobalConfig() string {
	if crushGlobal := os.Getenv("CRUSH_GLOBAL_CONFIG"); crushGlobal != "" {
		return filepath.Join(crushGlobal, fmt.Sprintf("%s.json", appName))
	}
	return filepath.Join(home.Config(), appName, fmt.Sprintf("%s.json", appName))
}

// GlobalThemesDir returns the directory where user Lua theme files live,
// alongside the global config file (e.g. ~/.config/crush/themes).
func GlobalThemesDir() string {
	return filepath.Join(filepath.Dir(GlobalConfig()), "themes")
}

// GlobalCacheDir returns the path to the global cache directory for the
// application.
func GlobalCacheDir() string {
	if crushCache := os.Getenv("CRUSH_CACHE_DIR"); crushCache != "" {
		return crushCache
	}
	if xdgCacheHome := os.Getenv("XDG_CACHE_HOME"); xdgCacheHome != "" {
		return filepath.Join(xdgCacheHome, appName)
	}
	if runtime.GOOS == "windows" {
		localAppData := cmp.Or(
			os.Getenv("LOCALAPPDATA"),
			filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local"),
		)
		return filepath.Join(localAppData, appName, "cache")
	}
	return filepath.Join(home.Dir(), ".cache", appName)
}

// ProjectConfigs returns list of current project configs paths.
func ProjectConfigs(cwd string) []string {
	return lookupConfigs(cwd)
}

// GlobalConfigData returns the path to the main data directory for the application.
// this config is used when the app overrides configurations instead of updating the global config.
func GlobalConfigData() string {
	if crushData := os.Getenv("CRUSH_GLOBAL_DATA"); crushData != "" {
		return filepath.Join(crushData, fmt.Sprintf("%s.json", appName))
	}
	if xdgDataHome := os.Getenv("XDG_DATA_HOME"); xdgDataHome != "" {
		return filepath.Join(xdgDataHome, appName, fmt.Sprintf("%s.json", appName))
	}

	// return the path to the main data directory
	// for windows, it should be in `%LOCALAPPDATA%/crush/`
	// for linux and macOS, it should be in `$HOME/.local/share/crush/`
	if runtime.GOOS == "windows" {
		localAppData := cmp.Or(
			os.Getenv("LOCALAPPDATA"),
			filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local"),
		)
		return filepath.Join(localAppData, appName, fmt.Sprintf("%s.json", appName))
	}

	return filepath.Join(home.Dir(), ".local", "share", appName, fmt.Sprintf("%s.json", appName))
}

// GlobalWorkspaceDir returns the path to the global server workspace
// directory. This directory acts as a meta-workspace for the server
// process, giving it a real workingDir so that config loading, scoped
// writes, and provider resolution behave identically to project
// workspaces.
func GlobalWorkspaceDir() string {
	return filepath.Dir(GlobalConfigData())
}

func assignIfNil[T any](ptr **T, val T) {
	if *ptr == nil {
		*ptr = &val
	}
}

func isInsideWorktree() bool {
	bts, err := exec.CommandContext(
		context.Background(),
		"git", "rev-parse",
		"--is-inside-work-tree",
	).CombinedOutput()
	return err == nil && strings.TrimSpace(string(bts)) == "true"
}

// worktreeRoot returns the absolute path of the canonical project root
// for dir — the directory under which `.crush/` (database, snapshot
// repo, managed worktrees) lives — or the empty string when dir is not
// inside a working tree (bare repositories, missing git binary, plain
// directories, or any other failure mode).
//
// The rule: the main working tree and every linked worktree (whether
// Crush-managed under `<main>/.crush/worktrees/<name>/` or user-created
// as a sibling, e.g. `~/m2` linked to `~/m`) all resolve to the main
// repo root. This ensures a single shared `.crush/` for the repository,
// preventing split database and snapshot state across worktrees.
//
// The effective working directory for tools (pwd, shell, file edits) is
// tracked separately from the project root and always reflects the cwd
// the user launched Crush from; see coordinator.effectiveWorkingDir.
//
// This determinism matters beyond local correctness: per the worktrees
// spec (docs/specs/WORKTREES_AND_SNAPSHOTS.md §1, §8) `.crush/` location
// defines the project, and the cloud sync model
// (docs/sync-spec.md §4) derives a project's identity/Durable-Object key
// from the git remote plus the repo-relative `.crush/` path. A stable,
// git-anchored project root keeps that fingerprint consistent.
func worktreeRoot(dir string) string {
	top, err := gitRevParse(dir, "--show-toplevel")
	if err != nil || top == "" {
		return ""
	}
	top = normalizePath(top)

	mainRoot, ok := gitMainWorktreeRoot(dir)
	if !ok || mainRoot == top {
		// Main worktree, or an unusual layout we couldn't classify
		// (bare repo, gitdir file, old git): treat dir's own
		// top-level as the project root.
		return top
	}

	// dir is any linked worktree — collapse to the main repo root so
	// .crush/ is always co-located with the main working tree.
	return mainRoot
}

// gitMainWorktreeRoot returns the working-tree root of the *main*
// repository backing dir, regardless of which linked worktree dir lives
// in. It derives the root from `--git-common-dir`, which always points
// at the main repo's `.git` directory. Returns ok=false for layouts it
// cannot classify (bare repos, a gitdir *file* rather than a `.git`
// directory, older git without `--git-common-dir`), leaving callers to
// fall back to the tree's own top-level.
func gitMainWorktreeRoot(dir string) (string, bool) {
	common, err := gitRevParse(dir, "--path-format=absolute", "--git-common-dir")
	if err != nil || common == "" {
		return "", false
	}
	common = normalizePath(common)
	if filepath.Base(common) != ".git" {
		return "", false
	}
	return filepath.Dir(common), true
}

// normalizePath returns an absolute, symlink-resolved form of p so that
// roots reported by separate git invocations (and the caller's resolved
// cwd) compare equal on platforms with symlinked temp dirs (e.g. macOS
// /tmp -> /private/tmp). Falls back to the cleaned absolute path when
// the target cannot be resolved.
func normalizePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// gitRevParse runs `git rev-parse <args...>` with cwd=dir and returns
// the trimmed stdout. Any non-zero exit produces an error.
func gitRevParse(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"rev-parse"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ProjectRoot returns the canonical project root for dir: the main git
// working tree root when dir is inside a git repository (including any
// linked worktree managed by Crush), or dir itself otherwise. This is
// the directory under which `.crush/` (database, private snapshot repo,
// managed worktrees) lives.
func ProjectRoot(dir string) string {
	if root := worktreeRoot(dir); root != "" {
		return root
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// projectBoundary returns the directory at which an upward configuration
// search rooted at dir should stop. It is the git working tree root when
// one can be detected, otherwise dir itself. Returning dir as a
// fallback keeps Crush from silently adopting state files placed above
// the current project.
func projectBoundary(dir string) string {
	if root := worktreeRoot(dir); root != "" {
		return root
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// GlobalSkillsDirs returns the default directories for Agent Skills.
// Skills in these directories are auto-discovered and their files can be read
// without permission prompts.
func GlobalSkillsDirs() []string {
	if crushSkills := os.Getenv("CRUSH_SKILLS_DIR"); crushSkills != "" {
		return []string{crushSkills}
	}

	paths := []string{
		filepath.Join(home.Config(), appName, "skills"),
		filepath.Join(home.Config(), "agents", "skills"),
		// Per the Agent Skills spec, scan ~/.agents/skills
		filepath.Join(home.Dir(), ".agents", "skills"),
		filepath.Join(home.Dir(), ".claude", "skills"),
	}

	// On Windows, also load from app data on top of `$HOME/.config/crush`.
	// This is here mostly for backwards compatibility.
	if runtime.GOOS == "windows" {
		appData := cmp.Or(
			os.Getenv("LOCALAPPDATA"),
			filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local"),
		)
		paths = append(
			paths,
			filepath.Join(appData, appName, "skills"),
			filepath.Join(appData, "agents", "skills"),
		)
	}

	return paths
}

// projectSkillSubdirs lists the conventional subdirectories where
// project-level skills are discovered. Shared across working-dir and
// git-root lookups to prevent drift when a new convention is added.
var projectSkillSubdirs = []string{
	".agents/skills",
	".crush/skills",
	".claude/skills",
	".cursor/skills",
}

// ProjectSkillsDir returns the default project directories for which Crush
// will look for skills. In addition to the working directory, it also
// checks the git working tree root so that monorepo-level skills are
// discovered when the user is inside a subdirectory.
// Working-directory paths come first so local skills take precedence
// over monorepo-level ones.
func ProjectSkillsDir(workingDir string) []string {
	dirs := make([]string, 0, len(projectSkillSubdirs)*2)
	for _, sub := range projectSkillSubdirs {
		dirs = append(dirs, filepath.Join(workingDir, sub))
	}

	// When the working directory is inside a git repository, also look at
	// the repository root so monorepo-level .agents/skills are found.
	if root := worktreeRoot(workingDir); root != "" && root != workingDir {
		for _, sub := range projectSkillSubdirs {
			dirs = append(dirs, filepath.Join(root, sub))
		}
	}

	return dirs
}

func isAppleTerminal() bool { return os.Getenv("TERM_PROGRAM") == "Apple_Terminal" }

// defaultSnapshotExclusions returns the default patterns to exclude from snapshots.
func defaultSnapshotExclusions() []string {
	return []string{
		"node_modules",
		"**/node_modules",
		"vendor",
		".venv",
		"venv",
		"__pycache__",
		"**/__pycache__",
		"*.pyc",
		"target",
		"dist",
		"build",
		".next",
		".nuxt",
		".output",
		".cache",
		"*.log",
		".DS_Store",
	}
}

// defaultPostCreateHooks returns the default commands to run after creating a worktree.
func defaultPostCreateHooks() []PostCreateHook {
	return []PostCreateHook{
		{IfExists: "bun.lockb", Run: "bun i"},
		{IfExists: "pnpm-lock.yaml", Run: "pnpm i"},
		{IfExists: "yarn.lock", Run: "yarn"},
		{IfExists: "package-lock.json", Run: "npm ci"},
		{IfExists: "go.sum", Run: "go mod download"},
		{IfExists: "Cargo.lock", Run: "cargo fetch"},
		{IfExists: "requirements.txt", Run: "pip install -r requirements.txt"},
	}
}

// normalizeHookEvent maps user-provided event names to their canonical
// form. Matching is case-insensitive and accepts snake_case variants
// (e.g. "pre_tool_use" → "PreToolUse").
func normalizeHookEvent(name string) string {
	switch strings.ToLower(strings.ReplaceAll(name, "_", "")) {
	case "pretooluse":
		return "PreToolUse"
	case "stop":
		return "Stop"
	default:
		return name
	}
}

// ValidateHooks normalizes event names and checks that every configured
// hook has a command and a syntactically valid matcher regex. Matcher
// compilation used for matching is owned by hooks.Runner; this function
// only validates up front so the user sees config errors at load time
// rather than on the first tool call.
func (c *Config) ValidateHooks() error {
	// Normalize event name keys.
	for event, eventHooks := range c.Hooks {
		canonical := normalizeHookEvent(event)
		if canonical != event {
			c.Hooks[canonical] = append(c.Hooks[canonical], eventHooks...)
			delete(c.Hooks, event)
		}
	}

	for event, eventHooks := range c.Hooks {
		for i, h := range eventHooks {
			if h.Command == "" {
				return fmt.Errorf("hook %s[%d]: command is required", event, i)
			}
			if h.Matcher == "" {
				continue
			}
			if _, err := regexp.Compile(h.Matcher); err != nil {
				return fmt.Errorf("hook %s[%d]: invalid matcher regex %q: %w", event, i, h.Matcher, err)
			}
		}
	}
	return nil
}
