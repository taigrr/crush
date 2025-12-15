package dialog

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/crush/internal/ui/common"
	uv "github.com/charmbracelet/ultraviolet"
)

// CloseKey is the default key binding to close dialogs.
var CloseKey = key.NewBinding(
	key.WithKeys("esc", "alt+esc"),
	key.WithHelp("esc", "exit"),
)

// Dialog is a component that can be displayed on top of the UI.
type Dialog interface {
	ID() string
	Update(msg tea.Msg) tea.Cmd
	View() string
}

// Overlay manages multiple dialogs as an overlay.
type Overlay struct {
	dialogs []Dialog
}

// NewOverlay creates a new [Overlay] instance.
func NewOverlay(dialogs ...Dialog) *Overlay {
	return &Overlay{
		dialogs: dialogs,
	}
}

// IsFrontDialog checks if the dialog with the specified ID is at the front.
func (d *Overlay) IsFrontDialog(dialogID string) bool {
	if len(d.dialogs) == 0 {
		return false
	}
	return d.dialogs[len(d.dialogs)-1].ID() == dialogID
}

// HasDialogs checks if there are any active dialogs.
func (d *Overlay) HasDialogs() bool {
	return len(d.dialogs) > 0
}

// ContainsDialog checks if a dialog with the specified ID exists.
func (d *Overlay) ContainsDialog(dialogID string) bool {
	for _, dialog := range d.dialogs {
		if dialog.ID() == dialogID {
			return true
		}
	}
	return false
}

// AddDialog adds a new dialog to the stack.
func (d *Overlay) AddDialog(dialog Dialog) {
	d.dialogs = append(d.dialogs, dialog)
}

// RemoveDialog removes the dialog with the specified ID from the stack.
func (d *Overlay) RemoveDialog(dialogID string) {
	for i, dialog := range d.dialogs {
		if dialog.ID() == dialogID {
			d.removeDialog(i)
			return
		}
	}
}

// Dialog returns the dialog with the specified ID, or nil if not found.
func (d *Overlay) Dialog(dialogID string) Dialog {
	for _, dialog := range d.dialogs {
		if dialog.ID() == dialogID {
			return dialog
		}
	}
	return nil
}

// BringToFront brings the dialog with the specified ID to the front.
func (d *Overlay) BringToFront(dialogID string) {
	for i, dialog := range d.dialogs {
		if dialog.ID() == dialogID {
			// Move the dialog to the end of the slice
			d.dialogs = append(d.dialogs[:i], d.dialogs[i+1:]...)
			d.dialogs = append(d.dialogs, dialog)
			return
		}
	}
}

// Update handles dialog updates.
func (d *Overlay) Update(msg tea.Msg) (*Overlay, tea.Cmd) {
	if len(d.dialogs) == 0 {
		return d, nil
	}

	idx := len(d.dialogs) - 1 // active dialog is the last one
	dialog := d.dialogs[idx]
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if key.Matches(msg, CloseKey) {
			// Close the current dialog
			d.removeDialog(idx)
			return d, nil
		}
	}

	if cmd := dialog.Update(msg); cmd != nil {
		// Close the current dialog
		d.removeDialog(idx)
		return d, cmd
	}

	return d, nil
}

// Draw renders the overlay and its dialogs.
func (d *Overlay) Draw(scr uv.Screen, area uv.Rectangle) {
	for _, dialog := range d.dialogs {
		view := dialog.View()
		viewWidth := lipgloss.Width(view)
		viewHeight := lipgloss.Height(view)
		center := common.CenterRect(area, viewWidth, viewHeight)
		if area.Overlaps(center) {
			uv.NewStyledString(view).Draw(scr, center)
		}
	}
}

// removeDialog removes a dialog from the stack.
func (d *Overlay) removeDialog(idx int) {
	if idx < 0 || idx >= len(d.dialogs) {
		return
	}
	d.dialogs = append(d.dialogs[:idx], d.dialogs[idx+1:]...)
}
