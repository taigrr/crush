package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/taigrr/crush/internal/agent"
	mcptools "github.com/taigrr/crush/internal/agent/tools/mcp"
	"github.com/taigrr/crush/internal/commands"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/oauth"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/skills"
)

// publishConfigChanged publishes a ConfigChanged event on the workspace's
// event broker so all subscribers (e.g. remote clients) refresh their
// cached config snapshot.
func publishConfigChanged(ws *Workspace) {
	if ws == nil || ws.App == nil {
		return
	}
	ws.SendEvent(pubsub.Event[proto.ConfigChanged]{
		Type:    pubsub.UpdatedEvent,
		Payload: proto.ConfigChanged{WorkspaceID: ws.ID},
	})
}

// MCPResourceContents holds the contents of an MCP resource returned
// by the backend.
type MCPResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     []byte `json:"blob,omitempty"`
}

// ReloadConfig re-reads the workspace's config files from disk and
// refreshes the in-memory config, providers, and agents. Clients are
// notified via a ConfigChanged event.
func (b *Backend) ReloadConfig(workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if ws.Cfg == nil {
		return fmt.Errorf("workspace has no config")
	}
	if err := ws.Cfg.ReloadFromDisk(b.ctx); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// reloadWorkspaceConfig refreshes a persisted workspace's config from
// disk when a client (re)connects to a deduplicated workspace. The
// server outlives its clients, so a workspace can be handed back to a
// reconnecting client with config that has since changed on disk. We
// only reload when the tracked config files are actually stale to avoid
// reconfiguring providers on every connect. Best-effort: failures are
// logged, not fatal.
func (b *Backend) reloadWorkspaceConfig(ws *Workspace) {
	if ws == nil || ws.Cfg == nil {
		return
	}
	if !ws.Cfg.ConfigStaleness().Dirty {
		return
	}
	if err := ws.Cfg.ReloadFromDisk(b.ctx); err != nil {
		slog.Warn("Failed to reload workspace config on client connect",
			"workspace", ws.ID, "error", err)
		return
	}
	slog.Info("Reloaded workspace config from disk on client connect", "workspace", ws.ID)
	publishConfigChanged(ws)
}

// SetConfigField sets a key/value pair in the config file for the
// given scope.
func (b *Backend) SetConfigField(workspaceID string, scope config.Scope, key string, value any) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if err := ws.Cfg.SetConfigField(scope, key, value); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// RemoveConfigField removes a key from the config file for the given
// scope.
func (b *Backend) RemoveConfigField(workspaceID string, scope config.Scope, key string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if err := ws.Cfg.RemoveConfigField(scope, key); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// UpdatePreferredModel updates the preferred model for the given type
// and persists it to the config file at the given scope.
func (b *Backend) UpdatePreferredModel(workspaceID string, scope config.Scope, modelType config.SelectedModelType, model config.SelectedModel) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if err := ws.Cfg.UpdatePreferredModel(scope, modelType, model); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// SetCompactMode sets the compact mode setting and persists it.
func (b *Backend) SetCompactMode(workspaceID string, scope config.Scope, enabled bool) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if err := ws.Cfg.SetCompactMode(scope, enabled); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// SetProviderAPIKey sets the API key for a provider and persists it.
func (b *Backend) SetProviderAPIKey(workspaceID string, scope config.Scope, providerID string, apiKey any) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if err := ws.Cfg.SetProviderAPIKey(scope, providerID, apiKey); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// ImportCopilot attempts to import a GitHub Copilot token from disk.
func (b *Backend) ImportCopilot(workspaceID string) (*oauth.Token, bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, false, err
	}
	token, ok := ws.Cfg.ImportCopilot()
	if ok {
		publishConfigChanged(ws)
	}
	return token, ok, nil
}

// RefreshOAuthToken refreshes the OAuth token for a provider.
func (b *Backend) RefreshOAuthToken(ctx context.Context, workspaceID string, scope config.Scope, providerID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if err := ws.Cfg.RefreshOAuthToken(ctx, scope, providerID); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// ProjectNeedsInitialization checks whether the project in this
// workspace needs initialization.
func (b *Backend) ProjectNeedsInitialization(workspaceID string) (bool, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return false, err
	}
	return config.ProjectNeedsInitialization(ws.Cfg)
}

// MarkProjectInitialized marks the project as initialized.
func (b *Backend) MarkProjectInitialized(workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if err := config.MarkProjectInitialized(ws.Cfg); err != nil {
		return err
	}
	publishConfigChanged(ws)
	return nil
}

// InitializePrompt builds the initialization prompt for the workspace.
func (b *Backend) InitializePrompt(workspaceID string) (string, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return "", err
	}
	return agent.InitializePrompt(ws.Cfg)
}

// ReadSkill reads a skill's content by ID.
func (b *Backend) ReadSkill(ctx context.Context, workspaceID, skillID string) ([]byte, proto.SkillReadResult, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, proto.SkillReadResult{}, err
	}

	mgr := ws.Skills
	content, result, err := skills.ReadContent(
		mgr.ActiveSkills(), mgr.ResolvedPaths(), mgr.WorkingDir(), skillID,
	)
	if err != nil {
		return nil, proto.SkillReadResult{}, err
	}
	return content, proto.SkillReadResult{
		Name:        result.Name,
		Description: result.Description,
		Source:      string(result.Source),
		Builtin:     result.Builtin,
	}, nil
}

// ListSkills returns the effective visible skills for a workspace.
func (b *Backend) ListSkills(workspaceID string) ([]proto.SkillInfo, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	mgr := ws.Skills
	entries := skills.Catalog(mgr.ActiveSkills(), mgr.ResolvedPaths(), mgr.WorkingDir())
	result := make([]proto.SkillInfo, len(entries))
	for i, entry := range entries {
		result[i] = proto.SkillInfo{
			ID:            entry.ID,
			Name:          entry.Name,
			Description:   entry.Description,
			Label:         entry.Label,
			Source:        string(entry.Source),
			UserInvocable: entry.UserInvocable,
		}
	}
	return result, nil
}

// EnableDockerMCP validates Docker MCP availability, stages the
// configuration, starts the MCP client, and persists the config.
func (b *Backend) EnableDockerMCP(ctx context.Context, workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	mcpConfig, err := ws.Cfg.PrepareDockerMCPConfig()
	if err != nil {
		return err
	}

	if err := mcptools.InitializeSingle(ctx, config.DockerMCPName, ws.Cfg); err != nil {
		disableErr := mcptools.DisableSingle(ws.Cfg, config.DockerMCPName)
		delete(ws.Cfg.Config().MCP, config.DockerMCPName)
		return fmt.Errorf("failed to start docker MCP: %w", errors.Join(err, disableErr))
	}

	if err := ws.Cfg.PersistDockerMCPConfig(mcpConfig); err != nil {
		disableErr := mcptools.DisableSingle(ws.Cfg, config.DockerMCPName)
		delete(ws.Cfg.Config().MCP, config.DockerMCPName)
		return fmt.Errorf("docker MCP started but failed to persist configuration: %w", errors.Join(err, disableErr))
	}

	publishConfigChanged(ws)
	return nil
}

// DisableDockerMCP closes the Docker MCP client, removes the
// configuration, and persists the change.
func (b *Backend) DisableDockerMCP(workspaceID string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}

	if err := mcptools.DisableSingle(ws.Cfg, config.DockerMCPName); err != nil {
		return fmt.Errorf("failed to disable docker MCP: %w", err)
	}

	if err := ws.Cfg.DisableDockerMCP(); err != nil {
		return err
	}

	publishConfigChanged(ws)
	return nil
}

// RefreshMCPTools refreshes the tools for a named MCP server.
func (b *Backend) RefreshMCPTools(ctx context.Context, workspaceID, name string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	mcptools.RefreshTools(ctx, ws.Cfg, name)
	return nil
}

// MCPAuthenticate initiates the OAuth flow for a named MCP server. The
// browser and callback listener run in this (server) process, which for
// the local daemon is the same machine as the user.
func (b *Backend) MCPAuthenticate(ctx context.Context, workspaceID, name string) error {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return err
	}
	return mcptools.AuthenticateMCP(ctx, ws.Cfg, name)
}

// MCPPendingAuth returns the MCP servers awaiting OAuth authorization.
func (b *Backend) MCPPendingAuth(workspaceID string) []mcptools.PendingAuthServer {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil
	}
	return mcptools.PendingAuthMCPs(ws.Cfg)
}

// MCPAuthURL returns the current OAuth authorization URL for a named MCP
// server, or empty if none is in progress.
func (b *Backend) MCPAuthURL(workspaceID, name string) string {
	if _, err := b.GetWorkspace(workspaceID); err != nil {
		return ""
	}
	return mcptools.MCPAuthURL(name)
}

// ReadMCPResource reads a resource from a named MCP server.
func (b *Backend) ReadMCPResource(ctx context.Context, workspaceID, name, uri string) ([]MCPResourceContents, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return nil, err
	}
	contents, err := mcptools.ReadResource(ctx, ws.Cfg, name, uri)
	if err != nil {
		return nil, err
	}
	result := make([]MCPResourceContents, len(contents))
	for i, c := range contents {
		result[i] = MCPResourceContents{
			URI:      c.URI,
			MIMEType: c.MIMEType,
			Text:     c.Text,
			Blob:     c.Blob,
		}
	}
	return result, nil
}

// GetMCPPrompt retrieves a prompt from a named MCP server.
func (b *Backend) GetMCPPrompt(workspaceID, clientID, promptID string, args map[string]string) (string, error) {
	ws, err := b.GetWorkspace(workspaceID)
	if err != nil {
		return "", err
	}
	return commands.GetMCPPrompt(ws.Cfg, clientID, promptID, args)
}
