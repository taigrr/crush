package model

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/app"
	"github.com/charmbracelet/crush/internal/config"
	"github.com/charmbracelet/crush/internal/csync"
	"github.com/charmbracelet/crush/internal/lsp"
	"github.com/charmbracelet/crush/internal/ui/common"
	"github.com/charmbracelet/crush/internal/ui/styles"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// LSPInfo wraps LSP client information with diagnostic counts by severity.
type LSPInfo struct {
	app.LSPClientInfo
	Diagnostics map[protocol.DiagnosticSeverity]int
}

// lspInfo renders the LSP status section showing active LSP clients and their
// diagnostic counts.
func (m *UI) lspInfo(width, maxItems int, isSection bool) string {
	t := m.com.Styles
	lspConfigs := m.com.Config().LSP.Sorted()

	title := t.Subtle.Render("LSPs")
	if isSection {
		title = common.Section(t, title, width)
	}
	list := t.Subtle.Render("None")
	if len(lspConfigs) > 0 {
		list = lspList(t, lspConfigs, m.lspStates, m.com.App.LSPClients, width, maxItems)
	}

	return lipgloss.NewStyle().Width(width).Render(fmt.Sprintf("%s\n\n%s", title, list))
}

// lspDiagnostics formats diagnostic counts with appropriate icons and colors.
func lspDiagnostics(t *styles.Styles, diagnostics map[protocol.DiagnosticSeverity]int) string {
	errs := []string{}
	if diagnostics[protocol.SeverityError] > 0 {
		errs = append(errs, t.LSP.ErrorDiagnostic.Render(fmt.Sprintf("%s %d", styles.ErrorIcon, diagnostics[protocol.SeverityError])))
	}
	if diagnostics[protocol.SeverityWarning] > 0 {
		errs = append(errs, t.LSP.WarningDiagnostic.Render(fmt.Sprintf("%s %d", styles.WarningIcon, diagnostics[protocol.SeverityWarning])))
	}
	if diagnostics[protocol.SeverityHint] > 0 {
		errs = append(errs, t.LSP.HintDiagnostic.Render(fmt.Sprintf("%s %d", styles.HintIcon, diagnostics[protocol.SeverityHint])))
	}
	if diagnostics[protocol.SeverityInformation] > 0 {
		errs = append(errs, t.LSP.InfoDiagnostic.Render(fmt.Sprintf("%s %d", styles.InfoIcon, diagnostics[protocol.SeverityInformation])))
	}
	return strings.Join(errs, " ")
}

// lspList renders a list of LSP clients with their status and diagnostics,
// truncating to maxItems if needed. It iterates over lspConfigs (already sorted
// alphabetically) and looks up states from the states map.
func lspList(t *styles.Styles, lspConfigs []config.LSP, states map[string]app.LSPClientInfo, clients *csync.Map[string, *lsp.Client], width, maxItems int) string {
	if maxItems <= 0 {
		return ""
	}
	var renderedLsps []string

	for i, l := range lspConfigs {
		if i >= maxItems {
			break
		}

		var icon string
		title := l.Name
		var description string
		var diagnostics string

		if state, exists := states[l.Name]; exists {
			switch state.State {
			case lsp.StateStarting:
				icon = t.ItemBusyIcon.String()
				description = t.Subtle.Render("starting...")
			case lsp.StateReady:
				icon = t.ItemOnlineIcon.String()
				if client, ok := clients.Get(l.Name); ok {
					counts := client.GetDiagnosticCounts()
					lspErrs := map[protocol.DiagnosticSeverity]int{
						protocol.SeverityError:       counts.Error,
						protocol.SeverityWarning:     counts.Warning,
						protocol.SeverityHint:        counts.Hint,
						protocol.SeverityInformation: counts.Information,
					}
					diagnostics = lspDiagnostics(t, lspErrs)
				}
			case lsp.StateError:
				icon = t.ItemErrorIcon.String()
				description = t.Subtle.Render("error")
				if state.Error != nil {
					description = t.Subtle.Render(fmt.Sprintf("error: %s", state.Error.Error()))
				}
			case lsp.StateDisabled:
				icon = t.ItemOfflineIcon.Foreground(t.Muted.GetBackground()).String()
				description = t.Subtle.Render("inactive")
			default:
				icon = t.ItemOfflineIcon.String()
			}
		} else if l.LSP.Disabled {
			icon = t.ItemOfflineIcon.Foreground(t.Muted.GetBackground()).String()
			description = t.Subtle.Render("disabled")
		} else {
			icon = t.ItemOfflineIcon.String()
		}

		renderedLsps = append(renderedLsps, common.Status(t, common.StatusOpts{
			Icon:         icon,
			Title:        title,
			Description:  description,
			ExtraContent: diagnostics,
		}, width))
	}

	if len(renderedLsps) > maxItems {
		visibleItems := renderedLsps[:maxItems-1]
		remaining := len(lspConfigs) - maxItems
		visibleItems = append(visibleItems, t.Subtle.Render(fmt.Sprintf("…and %d more", remaining)))
		return lipgloss.JoinVertical(lipgloss.Left, visibleItems...)
	}
	return lipgloss.JoinVertical(lipgloss.Left, renderedLsps...)
}
