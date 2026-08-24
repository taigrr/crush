package model

import (
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/logo"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/ui/styles/themes"
	"github.com/taigrr/crush/internal/version"
)

// cacheSidebarLogo renders and caches the sidebar logo at the specified width.
func (m *UI) cacheSidebarLogo(width int) {
	m.sidebarLogo = renderLogo(m.com.Styles, true, m.com.IsHyper(), width)
}

// applyTheme replaces the active styles with the given theme, drops the
// shared markdown renderer cache, and refreshes every component that
// caches style data.
func (m *UI) applyTheme(s styles.Styles) {
	*m.com.Styles = s
	common.InvalidateMarkdownRendererCache()
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
func renderLogo(t *styles.Styles, compact, hyper bool, width int) string {
	return logo.Render(t.Logo.GradCanvas, version.Version, compact, logo.Opts{
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
