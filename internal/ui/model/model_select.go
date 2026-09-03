package model

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/catwalk/pkg/catwalk"
	"github.com/taigrr/crush/internal/agent/hyper"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/stringext"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/styles/themes"
	"github.com/taigrr/crush/internal/ui/util"
)

// substituteArgs replaces $ARG_NAME placeholders in content with actual values.
func substituteArgs(content string, args map[string]string) string {
	for name, value := range args {
		placeholder := "$" + name
		content = strings.ReplaceAll(content, placeholder, value)
	}
	return content
}

// refreshHyperAndRetrySelect returns a command that silently refreshes
// the Hyper OAuth token and then re-runs the model selection. If the
// refresh fails, the selection resumes with ReAuthenticate set so the
// OAuth dialog opens.
func (m *UI) refreshHyperAndRetrySelect(msg dialog.ActionSelectModel) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := m.com.Workspace.RefreshOAuthToken(ctx, config.ScopeGlobal, "hyper"); err != nil {
			slog.Warn("Hyper OAuth refresh failed, requesting re-auth", "error", err)
			msg.ReAuthenticate = true
		}
		return hyperRefreshDoneMsg{action: msg}
	}
}

// fetchHyperCredits returns a command that asynchronously fetches the
// remaining Hyper credits from the API.
func (m *UI) fetchHyperCredits() tea.Cmd {
	return func() tea.Msg {
		cfg := m.com.Config()
		if cfg == nil {
			return nil
		}
		providerCfg, ok := cfg.Providers.Get(hyper.Name)
		if !ok {
			return nil
		}
		apiKey, err := m.com.Workspace.Resolver().ResolveValue(providerCfg.APIKey)
		if err != nil || apiKey == "" {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		credits, err := hyper.FetchCredits(ctx, apiKey)
		if err != nil {
			slog.Error("Failed to fetch Hyper credits", "error", err)
			return nil
		}
		return creditsUpdatedMsg{credits: credits}
	}
}

// handleSelectModel performs the model selection after any provider
// pre-checks (such as a silent Hyper OAuth refresh) have completed.
func (m *UI) handleSelectModel(msg dialog.ActionSelectModel) tea.Cmd {
	var cmds []tea.Cmd

	// No busy guard: the config write below is immediate and the live model
	// apply is deferred server-side until the agent finishes its turn (see
	// app.UpdateAgentModel -> UpdateModelsWhenIdle), so changing the model
	// mid-run no longer errors with "agent busy".

	cfg := m.com.Config()
	if cfg == nil {
		return util.ReportError(errors.New("configuration not found"))
	}

	var (
		providerID   = msg.Model.Provider
		isCopilot    = providerID == string(catwalk.InferenceProviderCopilot)
		isConfigured = func() bool { _, ok := cfg.Providers.Get(providerID); return ok }
		isOnboarding = m.state == uiOnboarding
	)

	// For Hyper, if the stored OAuth token is expired, try a silent
	// refresh before deciding whether the provider is configured. Keeps
	// users from hitting a 401 on their first message after the
	// short-lived access token ages out.
	if !msg.ReAuthenticate && providerID == "hyper" {
		if pc, ok := cfg.Providers.Get(providerID); ok && pc.OAuthToken != nil && pc.OAuthToken.IsExpired() {
			return m.refreshHyperAndRetrySelect(msg)
		}
	}

	// Attempt to import GitHub Copilot tokens from VSCode if available.
	if isCopilot && !isConfigured() && !msg.ReAuthenticate {
		m.com.Workspace.ImportCopilot()
	}

	if !isConfigured() || msg.ReAuthenticate {
		m.dialog.CloseDialog(dialog.ModelsID)
		if cmd := m.openAuthenticationDialog(msg.Provider, msg.Model, msg.ModelType); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return tea.Batch(cmds...)
	}

	// Session-scoped selection: stamp the session row and stop. No
	// config is written, so nothing else (new sessions, workers,
	// sub-agents) changes. The Updated event re-renders the sidebar;
	// the coordinator resolves the stamp on the next turn.
	if msg.SessionID != "" {
		return m.applySessionModel(msg)
	}

	prevOrchestrator, hadPrevOrchestrator := cfg.Models[config.SelectedModelTypeOrchestrator]
	if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, msg.ModelType, msg.Model); err != nil {
		cmds = append(cmds, util.ReportError(err))
	} else {
		if msg.ModelType == config.SelectedModelTypeLarge {
			// Swap the theme live based on the newly selected large model's
			// provider, unless the user has pinned an explicit theme.
			var themeName string
			if cfg.Options != nil && cfg.Options.TUI != nil {
				themeName = cfg.Options.TUI.Theme
			}
			if themeName == "" {
				m.applyTheme(themes.ThemeForProvider(providerID, m.hasDarkBackground))
			}
		}
		if _, ok := cfg.Models[config.SelectedModelTypeSmall]; !ok {
			// Ensure small model is set is unset.
			smallModel := m.com.Workspace.GetDefaultSmallModel(providerID)
			if err := m.com.Workspace.UpdatePreferredModel(config.ScopeGlobal, config.SelectedModelTypeSmall, smallModel); err != nil {
				cmds = append(cmds, util.ReportError(err))
			}
		}
	}

	// A new orchestrator should be felt now, not only in the next
	// session: re-stamp the open session when it is still following the
	// previous orchestrator (or has no stamp and none was configured).
	restamp := msg.ModelType == config.SelectedModelTypeOrchestrator && m.followsOrchestrator(prevOrchestrator, hadPrevOrchestrator)

	cmds = append(cmds, func() tea.Msg {
		if err := m.com.Workspace.UpdateAgentModel(context.TODO()); err != nil {
			return util.ReportError(err)
		}

		modelType := stringext.Capitalize(string(msg.ModelType))
		modelMsg := fmt.Sprintf("%s model changed to %s", modelType, m.displayModelName(msg.Model))
		if msg.ModelType == config.SelectedModelTypeOrchestrator && !restamp {
			modelMsg += " (applies to sessions you open from now on)"
		}

		return util.NewInfoMsg(modelMsg)
	})
	if restamp {
		sel := msg.Model
		sessionID := m.session.ID
		cmds = append(cmds, func() tea.Msg {
			if err := m.com.Workspace.SetSessionModel(context.Background(), sessionID, &sel); err != nil {
				return util.ReportError(err)()
			}
			return nil
		})
	}

	m.dialog.CloseDialog(dialog.APIKeyInputID)
	m.dialog.CloseDialog(dialog.OAuthID)
	m.dialog.CloseDialog(dialog.ModelsID)

	if isOnboarding {
		m.setState(uiLanding, uiFocusEditor)
		m.com.Config().SetupAgents()
		if err := m.com.Workspace.InitCoderAgent(context.TODO()); err != nil {
			cmds = append(cmds, util.ReportError(err))
		}
	} else if m.com.IsHyper() {
		cmds = append(cmds, m.fetchHyperCredits())
	}

	return tea.Batch(cmds...)
}

func (m *UI) openAuthenticationDialog(provider catwalk.Provider, model config.SelectedModel, modelType config.SelectedModelType) tea.Cmd {
	var (
		dlg dialog.Dialog
		cmd tea.Cmd

		isOnboarding = m.state == uiOnboarding
	)

	switch provider.ID {
	case "hyper":
		dlg, cmd = dialog.NewOAuthHyper(m.com, isOnboarding, provider, model, modelType)
	case catwalk.InferenceProviderCopilot:
		dlg, cmd = dialog.NewOAuthCopilot(m.com, isOnboarding, provider, model, modelType)
	default:
		dlg, cmd = dialog.NewAPIKeyInput(m.com, isOnboarding, provider, model, modelType)
	}

	if m.dialog.ContainsDialog(dlg.ID()) {
		m.dialog.BringToFront(dlg.ID())
		return nil
	}

	m.dialog.OpenDialogWithGrace(dlg)
	return cmd
}

// displayModelName prefers the catalog's display name for a selection.
func (m *UI) displayModelName(sel config.SelectedModel) string {
	if cfg := m.com.Config(); cfg != nil {
		if cw := cfg.GetModel(sel.Provider, sel.Model); cw != nil && cw.Name != "" {
			return cw.Name
		}
	}
	return sel.Model
}

// followsOrchestrator reports whether the open session is running the
// orchestrator by default rather than a deliberate one-off pick: it has
// no stamp and no orchestrator was configured, or its stamp equals the
// orchestrator that was configured before the change.
func (m *UI) followsOrchestrator(prev config.SelectedModel, hadPrev bool) bool {
	if !m.hasSession() {
		return false
	}
	cur := m.session.Model
	switch {
	case cur == nil:
		return !hadPrev
	case hadPrev:
		return cur.Provider == prev.Provider && cur.Model == prev.Model
	}
	return false
}

// applySessionModel stamps the selection on the session named by msg.
// Picking the workspace's large model clears the stamp instead of
// pinning it, so the session follows the Large default again.
func (m *UI) applySessionModel(msg dialog.ActionSelectModel) tea.Cmd {
	cfg := m.com.Config()
	sel := msg.Model
	stamp := &sel
	if large, ok := cfg.Models[config.SelectedModelTypeLarge]; ok &&
		large.Provider == sel.Provider && large.Model == sel.Model {
		stamp = nil
	}
	m.dialog.CloseDialog(dialog.ModelsID)
	name := m.displayModelName(sel)
	return func() tea.Msg {
		if err := m.com.Workspace.SetSessionModel(context.Background(), msg.SessionID, stamp); err != nil {
			return util.ReportError(err)()
		}
		if stamp == nil {
			return util.NewInfoMsg("This session now follows the Large model (" + name + ")")
		}
		return util.NewInfoMsg("This session now runs " + name)
	}
}
