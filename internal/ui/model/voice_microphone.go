package model

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/voice"
)

// micDevicesMsg carries the result of enumerating input devices so the
// picker can open with a populated list.
type micDevicesMsg struct {
	devices []voice.InputDevice
	err     error
}

// listMicrophones enumerates input devices off the update loop.
func (m *UI) listMicrophones() tea.Cmd {
	return func() tea.Msg {
		devices, err := voice.ListInputDevices()
		return micDevicesMsg{devices: devices, err: err}
	}
}

// openMicrophoneDialog kicks off device enumeration; the dialog itself
// opens when micDevicesMsg arrives.
func (m *UI) openMicrophoneDialog() tea.Cmd {
	if m.dialog.ContainsDialog(dialog.MicrophoneID) {
		m.dialog.BringToFront(dialog.MicrophoneID)
		return nil
	}
	return m.listMicrophones()
}

func (m *UI) handleMicDevices(msg micDevicesMsg) tea.Cmd {
	if msg.err != nil {
		return util.ReportError(msg.err)
	}
	if m.dialog.ContainsDialog(dialog.MicrophoneID) {
		m.dialog.BringToFront(dialog.MicrophoneID)
		return nil
	}
	m.dialog.OpenDialog(dialog.NewMicrophone(m.com, msg.devices, m.com.Config().VoiceInputDevice()))
	return nil
}

// selectMicrophone persists the chosen device and restarts the voice
// pipeline so the next recording opens it. An in-flight recording is
// stopped first; its interim text is committed to the prompt.
func (m *UI) selectMicrophone(msg dialog.ActionSelectMicrophone) tea.Cmd {
	cfg := m.com.Config()
	if cfg == nil {
		return util.ReportError(errors.New("configuration not found"))
	}
	if m.voice != nil {
		m.voice.dict.commit()
		m.voice.reset()
	}
	var err error
	if msg.DeviceID == "" {
		err = m.com.Workspace.RemoveConfigField(config.ScopeGlobal, "options.voice.input_device")
	} else {
		err = m.com.Workspace.SetConfigField(config.ScopeGlobal, "options.voice.input_device", msg.DeviceID)
	}
	if err != nil {
		return util.ReportError(err)
	}
	if cfg.Options != nil {
		if cfg.Options.Voice == nil {
			cfg.Options.Voice = &config.VoiceOptions{}
		}
		cfg.Options.Voice.InputDevice = msg.DeviceID
	}
	name := msg.Name
	if msg.DeviceID == "" {
		name = "System Default"
	}
	return util.ReportInfo("Microphone set to " + name)
}
