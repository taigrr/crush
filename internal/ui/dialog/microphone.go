package dialog

import (
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/list"
	"github.com/taigrr/crush/internal/ui/styles"
	"github.com/taigrr/crush/internal/voice"
)

const (
	// MicrophoneID is the identifier for the microphone picker dialog.
	MicrophoneID              = "microphone"
	microphoneDialogMaxWidth  = 64
	microphoneDialogMaxHeight = 14
)

// Microphone is a dialog for choosing the dictation input device.
type Microphone struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// MicrophoneItem is one input device in the picker. An empty device ID
// is the synthetic "System Default" entry.
type MicrophoneItem struct {
	*list.Versioned
	device    voice.InputDevice
	isCurrent bool
	t         *styles.Styles
	m         fuzzy.Match
	cache     map[int]string
	focused   bool
}

// Finished implements list.Item.
func (i *MicrophoneItem) Finished() bool { return true }

var (
	_ Dialog   = (*Microphone)(nil)
	_ ListItem = (*MicrophoneItem)(nil)
)

// NewMicrophone builds the picker from an already-enumerated device
// list. current is the configured [voice.InputDevice.ID] ("" for the
// system default).
func NewMicrophone(com *common.Common, devices []voice.InputDevice, current string) *Microphone {
	d := &Microphone{com: com}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	d.help = h

	d.list = list.NewFilterableList()
	d.list.Focus()

	d.input = textinput.New()
	d.input.SetVirtualCursor(false)
	d.input.Placeholder = "Type to filter"
	d.input.SetStyles(com.Styles.TextInput)
	d.input.Focus()

	d.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	d.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	d.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	d.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	d.keyMap.Close = CloseKey

	d.setItems(devices, current)
	return d
}

// ID implements Dialog.
func (d *Microphone) ID() string { return MicrophoneID }

// HandleMsg implements [Dialog].
func (d *Microphone) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, d.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, d.keyMap.Previous):
			d.list.Focus()
			if d.list.IsSelectedFirst() {
				d.list.SelectLast()
				d.list.ScrollToBottom()
				break
			}
			d.list.SelectPrev()
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Next):
			d.list.Focus()
			if d.list.IsSelectedLast() {
				d.list.SelectFirst()
				d.list.ScrollToTop()
				break
			}
			d.list.SelectNext()
			d.list.ScrollToSelected()
		case key.Matches(msg, d.keyMap.Select):
			selected := d.list.SelectedItem()
			if selected == nil {
				break
			}
			item, ok := selected.(*MicrophoneItem)
			if !ok {
				break
			}
			return ActionSelectMicrophone{DeviceID: item.device.ID, Name: item.device.Name}
		default:
			var cmd tea.Cmd
			d.input, cmd = d.input.Update(msg)
			d.list.SetFilter(d.input.Value())
			d.list.ScrollToTop()
			d.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (d *Microphone) Cursor() *tea.Cursor {
	return InputCursor(d.com.Styles, d.input.Cursor())
}

// Draw implements [Dialog].
func (d *Microphone) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := d.com.Styles
	width := max(0, min(microphoneDialogMaxWidth, area.Dx()))
	height := max(0, min(microphoneDialogMaxHeight, area.Dy()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()

	d.input.SetWidth(innerWidth - t.Dialog.InputPrompt.GetHorizontalFrameSize() - 1)
	d.list.SetSize(innerWidth, height-heightOffset)
	d.help.SetWidth(innerWidth)

	rc := NewRenderContext(t, width)
	rc.Title = "Select Microphone"
	rc.AddPart(t.Dialog.InputPrompt.Render(d.input.View()))

	visibleCount := len(d.list.FilteredItems())
	if d.list.Height() >= visibleCount {
		d.list.ScrollToTop()
	} else {
		d.list.ScrollToSelected()
	}

	rc.AddPart(t.Dialog.List.Height(d.list.Height()).Render(d.list.Render()))
	rc.Help = d.help.View(d)

	view := rc.Render()
	cur := d.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (d *Microphone) ShortHelp() []key.Binding {
	return []key.Binding{d.keyMap.UpDown, d.keyMap.Select, d.keyMap.Close}
}

// FullHelp implements [help.KeyMap].
func (d *Microphone) FullHelp() [][]key.Binding {
	return [][]key.Binding{{d.keyMap.Select, d.keyMap.Next, d.keyMap.Previous, d.keyMap.Close}}
}

func (d *Microphone) setItems(devices []voice.InputDevice, current string) {
	all := make([]voice.InputDevice, 0, len(devices)+1)
	all = append(all, voice.InputDevice{ID: "", Name: "System Default"})
	all = append(all, devices...)

	items := make([]list.FilterableItem, 0, len(all))
	selectedIndex := 0
	for i, dev := range all {
		item := &MicrophoneItem{
			Versioned: list.NewVersioned(),
			device:    dev,
			isCurrent: dev.ID == current,
			t:         d.com.Styles,
		}
		items = append(items, item)
		if dev.ID == current {
			selectedIndex = i
		}
	}
	d.list.SetItems(items...)
	d.list.SetSelected(selectedIndex)
	d.list.ScrollToSelected()
}

// Filter implements list.FilterableItem.
func (i *MicrophoneItem) Filter() string { return i.device.Name }

// ID implements list.Item.
func (i *MicrophoneItem) ID() string {
	if i.device.ID == "" {
		return "__default__"
	}
	return i.device.ID
}

// SetFocused implements ListItem.
func (i *MicrophoneItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = nil
	i.focused = focused
	if i.Versioned != nil {
		i.Bump()
	}
}

// SetMatch implements ListItem.
func (i *MicrophoneItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(i.m, m) {
		return
	}
	i.cache = nil
	i.m = m
	if i.Versioned != nil {
		i.Bump()
	}
}

// Render implements list.Item.
func (i *MicrophoneItem) Render(width int) string {
	var info string
	switch {
	case i.isCurrent && i.device.Default:
		info = "current · os default"
	case i.isCurrent:
		info = "current"
	case i.device.Default:
		info = "os default"
	}
	st := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(st, i.device.Name, info, i.focused, width, i.cache, &i.m)
}
