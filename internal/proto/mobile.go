package proto

import "github.com/taigrr/crush/internal/config"

// SessionModel is the model a session's turns run on.
type SessionModel struct {
	// ModelRef is the session's own model reference; empty when the
	// session follows the workspace's large model.
	ModelRef string `json:"model_ref,omitempty"`
	// Large is the model the session's top-level turns run on: the
	// resolved ModelRef, else the workspace's large model.
	Large *config.SelectedModel `json:"large,omitempty"`
	// Small is the model used for the session's titles and summaries. It
	// is always the workspace's small model.
	Small *config.SelectedModel `json:"small,omitempty"`
	// Default reports that the session follows the workspace's large
	// model (no ModelRef, or one that no longer resolves).
	Default bool `json:"default"`
} //@name proto.SessionModel

// SessionModelRequest sets the model a session runs on. Exactly one of
// ModelRef and Model may be given; neither clears the session's model so
// it follows the workspace's large model again.
type SessionModelRequest struct {
	// ModelType is the slot to set; only "large" (the default) is
	// per-session.
	ModelType config.SelectedModelType `json:"model_type,omitempty"`
	// ModelRef is a role name, "provider/model", or a bare model id.
	ModelRef string `json:"model_ref,omitempty"`
	// Model selects a provider and model explicitly.
	Model *config.SelectedModel `json:"model,omitempty"`
} //@name proto.SessionModelRequest

// ProviderInfo is a secret-free view of a provider for clients.
type ProviderInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	// Configured reports that the workspace config has this provider.
	Configured bool `json:"configured"`
	Disabled   bool `json:"disabled,omitempty"`
	// HasCredential reports that an API key or OAuth token is set; the
	// credential itself is never returned.
	HasCredential bool `json:"has_credential"`
	// OAuth reports that the credential is an OAuth token.
	OAuth               bool                `json:"oauth,omitempty"`
	DefaultLargeModelID string              `json:"default_large_model_id,omitempty"`
	DefaultSmallModelID string              `json:"default_small_model_id,omitempty"`
	Models              []ProviderModelInfo `json:"models"`
} //@name proto.ProviderInfo

// ProviderModelInfo describes one of a provider's models.
type ProviderModelInfo struct {
	ID                     string   `json:"id"`
	Name                   string   `json:"name"`
	ContextWindow          int64    `json:"context_window,omitempty"`
	DefaultMaxTokens       int64    `json:"default_max_tokens,omitempty"`
	CanReason              bool     `json:"can_reason,omitempty"`
	ReasoningLevels        []string `json:"reasoning_levels,omitempty"`
	DefaultReasoningEffort string   `json:"default_reasoning_effort,omitempty"`
	SupportsImages         bool     `json:"supports_attachments,omitempty"`
	CostPer1MIn            float64  `json:"cost_per_1m_in,omitempty"`
	CostPer1MOut           float64  `json:"cost_per_1m_out,omitempty"`
} //@name proto.ProviderModelInfo

// VoiceClientSecretRequest is the POST /v1/voice/client-secret body.
type VoiceClientSecretRequest struct {
	SessionID string `json:"session_id"`
} //@name proto.VoiceClientSecretRequest

// VoiceClientSecret is a short-lived realtime voice credential.
type VoiceClientSecret struct {
	Value string `json:"value"`
	// ExpiresAt is the secret's expiry in unix seconds.
	ExpiresAt int64 `json:"expires_at"`
} //@name proto.VoiceClientSecret
