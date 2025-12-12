package dialog

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// CloseKey is the default key binding to close dialogs.
var CloseKey = key.NewBinding(
	key.WithKeys("esc", "alt+esc"),
	key.WithHelp("esc", "exit"),
)

// OverlayKeyMap defines key bindings for dialogs.
type OverlayKeyMap struct {
	Close key.Binding
}

// ActionType represents the type of action taken by a dialog.
type ActionType int

const (
	// ActionNone indicates no action.
	ActionNone ActionType = iota
	// ActionClose indicates that the dialog should be closed.
	ActionClose
	// ActionSelect indicates that an item has been selected.
	ActionSelect
)

// Action represents an action taken by a dialog.
// It can be used to signal closing or other operations.
type Action struct {
	Type    ActionType
	Payload any
}

// Dialog is a component that can be displayed on top of the UI.
type Dialog interface {
	ID() string
	Update(msg tea.Msg) (Action, tea.Cmd)
	Layer() *lipgloss.Layer
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

	action, cmd := dialog.Update(msg)
	switch action.Type {
	case ActionClose:
		// Close the current dialog
		d.removeDialog(idx)
		return d, cmd
	case ActionSelect:
		// Pass the action up (without modifying the dialog stack)
		return d, cmd
	}

	return d, cmd
}

// Layers returns the current stack of dialogs as lipgloss layers.
func (d *Overlay) Layers() []*lipgloss.Layer {
	layers := make([]*lipgloss.Layer, len(d.dialogs))
	for i, dialog := range d.dialogs {
		layers[i] = dialog.Layer()
	}
	return layers
}

// removeDialog removes a dialog from the stack.
func (d *Overlay) removeDialog(idx int) {
	if idx < 0 || idx >= len(d.dialogs) {
		return
	}
	d.dialogs = append(d.dialogs[:idx], d.dialogs[idx+1:]...)
}
