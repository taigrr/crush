package model

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/ui/dialog"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/voice"
)

type micDevicesMsg struct {
	devices []voice.InputDevice
	err     error
}

func (m *UI) listMicrophones() tea.Cmd {
	return func() tea.Msg {
		devices, err := voice.ListInputDevices()
		return micDevicesMsg{devices: devices, err: err}
	}
}

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
