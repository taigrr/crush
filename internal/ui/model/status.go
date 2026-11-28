package model

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/util"
)

// DefaultStatusTTL is the default time-to-live for status messages.
const DefaultStatusTTL = 5 * time.Second

// Status is the status bar and help model.
type Status struct {
	com      *common.Common
	hideHelp bool
	line     string
	indent   int
	help     help.Model
	helpKm   help.KeyMap
	msg      util.InfoMsg
}

// NewStatus creates a new status bar and help model.
func NewStatus(com *common.Common, km help.KeyMap) *Status {
	s := new(Status)
	s.com = com
	s.help = help.New()
	s.help.Styles = com.Styles.Help
	s.helpKm = km
	return s
}

// SetInfoMsg sets the status info message.
func (s *Status) SetInfoMsg(msg util.InfoMsg) {
	s.msg = msg
}

// ClearInfoMsg clears the status info message.
func (s *Status) ClearInfoMsg() {
	s.msg = util.InfoMsg{}
}

// SetWidth sets the width of the status bar and help view.
func (s *Status) SetWidth(width int) {
	helpStyle := s.com.Styles.Status.Help
	horizontalPadding := helpStyle.GetPaddingLeft() + helpStyle.GetPaddingRight()
	s.help.SetWidth(width - horizontalPadding)
}

// ShowingAll returns whether the full help view is shown.
func (s *Status) ShowingAll() bool {
	return s.help.ShowAll
}

// ToggleHelp toggles the full help view.
func (s *Status) ToggleHelp() {
	s.help.ShowAll = !s.help.ShowAll
}

// SetHideHelp sets whether the app is on the onboarding flow.
func (s *Status) SetHideHelp(hideHelp bool) {
	s.hideHelp = hideHelp
}

// SetLine replaces the help row with a single status line. An empty line
// restores the help view.
func (s *Status) SetLine(line string, indent int) {
	s.line = line
	s.indent = indent
}

// minimalStatusLine is the gx footer: an optional yolo mark, the model,
// and the reasoning effort, dimmed and separated by middle dots.
func (m *UI) minimalStatusLine(width int) string {
	t := m.com.Styles
	var parts []string
	if m.yoloMode {
		parts = append(parts, "Yolo")
	}
	model := m.effectiveModel()
	if model != nil && model.CatwalkCfg.Name != "" {
		parts = append(parts, model.CatwalkCfg.Name)
	}
	if model != nil {
		if effort := reasoningInfoFor(model); effort != "" {
			parts = append(parts, effort)
		}
	}
	line := strings.Join(parts, " · ")
	if width > 0 {
		line = ansi.Truncate(line, width, "…")
	}
	return t.Resource.AdditionalText.Render(line)
}

// Draw draws the status bar onto the screen.
func (s *Status) Draw(scr uv.Screen, area uv.Rectangle) {
	if s.line != "" {
		uv.NewStyledString(s.line).Draw(scr, area)
	} else if !s.hideHelp {
		helpView := s.com.Styles.Status.Help.Render(s.help.View(s.helpKm))
		uv.NewStyledString(helpView).Draw(scr, area)
	}

	// Render notifications
	if s.msg.IsEmpty() {
		return
	}

	var indStyle lipgloss.Style
	var msgStyle lipgloss.Style
	switch s.msg.Type {
	case util.InfoTypeError:
		indStyle = s.com.Styles.Status.ErrorIndicator
		msgStyle = s.com.Styles.Status.ErrorMessage
	case util.InfoTypeWarn:
		indStyle = s.com.Styles.Status.WarnIndicator
		msgStyle = s.com.Styles.Status.WarnMessage
	case util.InfoTypeUpdate:
		indStyle = s.com.Styles.Status.UpdateIndicator
		msgStyle = s.com.Styles.Status.UpdateMessage
	case util.InfoTypeInfo:
		indStyle = s.com.Styles.Status.InfoIndicator
		msgStyle = s.com.Styles.Status.InfoMessage
	case util.InfoTypeSuccess:
		indStyle = s.com.Styles.Status.SuccessIndicator
		msgStyle = s.com.Styles.Status.SuccessMessage
	}

	if s.line != "" {
		indStyle = indStyle.Padding(0, 1)
		msgStyle = msgStyle.Padding(0, 1)
	}
	ind := indStyle.String()
	indWidth := lipgloss.Width(ind)
	msgPad := msgStyle.GetPaddingLeft() + msgStyle.GetPaddingRight()
	indent := 0
	if s.line != "" {
		indent = min(max(0, s.indent), max(0, area.Dx()-1))
	}
	avail := max(0, area.Dx()-indent-indWidth-msgPad)
	msg := strings.Join(strings.Split(s.msg.Msg, "\n"), " ")
	msg = ansi.Truncate(msg, avail, "…")
	if w := lipgloss.Width(msg); w < avail {
		msg += strings.Repeat(" ", avail-w)
	}
	info := msgStyle.Render(msg)

	// Draw the info message over the help view. Minimal mode paints
	// exactly one row so the background cannot fill a taller area.
	banner := ind + info
	if s.line != "" {
		banner = strings.Repeat(" ", indent) + strings.Split(banner, "\n")[0]
		area.Min.Y = area.Max.Y - 1
		area.Max.Y = area.Min.Y + 1
	}
	uv.NewStyledString(banner).Draw(scr, area)
}

// clearInfoMsgCmd returns a command that clears the info message after the
// given TTL.
func clearInfoMsgCmd(ttl time.Duration) tea.Cmd {
	return tea.Tick(ttl, func(time.Time) tea.Msg {
		return util.ClearStatusMsg{}
	})
}
