package model

import (
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/logo"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/ui/styles/themes"
)

// cacheSidebarLogo renders and caches the sidebar logo at the specified width.
func (m *UI) cacheSidebarLogo(width int) {
	m.sidebarLogoLabel = m.versionLabel()
	m.sidebarLogoWidth = width
	m.sidebarLogo = renderLogo(m.com.Styles, m.sidebarLogoLabel, true, m.com.IsHyper(), width)
}

// applyTheme replaces the active styles with the given theme, drops the
// shared markdown renderer cache, and refreshes every component that
// caches style data.
func (m *UI) applyTheme(s styles.Styles) {
	if m.baseStyles == nil {
		m.baseStyles = m.com.Styles
	}
	*m.baseStyles = s
	common.InvalidateMarkdownRendererCache()
	m.applyMinimalStyles()
}

// applyMinimalStyles points com.Styles at the theme, or at a flattened
// copy of it while minimal mode is on. Subcomponents that copied styles
// at construction pick up the change through refreshStyles.
func (m *UI) applyMinimalStyles() {
	if m.com == nil || m.com.Styles == nil {
		return
	}
	if m.baseStyles == nil {
		m.baseStyles = m.com.Styles
	}
	next := m.baseStyles
	if m.minimalMode {
		next = m.baseStyles.WithMinimal(true)
	}
	changed := m.com.Styles != next
	if changed {
		m.com.Styles = next
	}
	if changed && m.chat != nil {
		m.chat.SetItemStyles(m.com.Styles)
	}
	// NewUI calls this before status, chat, and the editor exist.
	if m.status == nil || m.header == nil || m.completions == nil {
		return
	}
	m.refreshStyles()
}

func (m *UI) applyConfiguredTheme() {
	cfg := m.com.Config()
	var themeName, providerID string
	if cfg != nil {
		if cfg.Options != nil && cfg.Options.TUI != nil {
			themeName = cfg.Options.TUI.Theme
		}
		providerID = cfg.Models[config.SelectedModelTypeLarge].Provider
	}
	if m.activeThemeName != "" {
		themeName = m.activeThemeName
	}
	m.applyTheme(themes.ResolveTheme(themeName, config.GlobalThemesDir(), providerID, m.hasDarkBackground))
}

// refreshStyles pushes the current *m.com.Styles into every subcomponent
// that copies or pre-renders style-dependent values at construction time.
func (m *UI) refreshStyles() {
	t := m.com.Styles
	m.header.refresh()
	if m.layout.sidebar.Dx() > 0 {
		m.cacheSidebarLogo(m.layout.sidebar.Dx())
	}
	m.textarea.SetStyles(t.Editor.Textarea)
	m.completions.SetStyles(t.Completions.Normal, t.Completions.Focused, t.Completions.Match)
	m.attachments.Renderer().SetStyles(
		t.Attachments.Normal,
		t.Attachments.Deleting,
		t.Attachments.Image,
		t.Attachments.Text,
		t.Attachments.Skill,
	)
	m.todoSpinner.Style = t.Pills.TodoSpinner
	m.status.help.Styles = t.Help
	m.chat.InvalidateRenderCaches()
}

// renderLogo renders the Crush logo with the given styles and dimensions.
func renderLogo(t *styles.Styles, ver string, compact, hyper bool, width int) string {
	return logo.Render(t.Logo.GradCanvas, ver, compact, logo.Opts{
		FieldColor:   t.Logo.FieldColor,
		TitleColorA:  t.Logo.TitleColorA,
		TitleColorB:  t.Logo.TitleColorB,
		CharmColor:   t.Logo.CharmColor,
		VersionColor: t.Logo.VersionColor,
		Edition:      logoEdition,
		Width:        width,
		Hyper:        hyper,
	})
}
