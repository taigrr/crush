package model

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const voicePulseInterval = 550 * time.Millisecond

type voicePulseTickMsg struct {
	gen int
}

func voicePulseTickCmd(gen int) tea.Cmd {
	return tea.Tick(voicePulseInterval, func(time.Time) tea.Msg {
		return voicePulseTickMsg{gen: gen}
	})
}

func (m *UI) startVoicePulse() tea.Cmd {
	m.voice.pulseGen++
	m.voice.pulseOn = true
	if m.lowBandwidthEnabled() {
		return nil
	}
	return voicePulseTickCmd(m.voice.pulseGen)
}

func (m *UI) handleVoicePulseTick(msg voicePulseTickMsg) tea.Cmd {
	if m.voice == nil || msg.gen != m.voice.pulseGen || !m.voice.dict.listening() {
		return nil
	}
	m.voice.pulseOn = !m.voice.pulseOn
	return voicePulseTickCmd(msg.gen)
}

func (m *UI) voiceIndicatorVisible() bool {
	return m.voice != nil && m.voice.dict.active()
}

func (m *UI) renderVoiceIndicator(width int) string {
	if !m.voiceIndicatorVisible() || width <= 0 {
		return ""
	}
	sty := m.com.Styles.Editor
	dot := sty.VoiceRecordingDotOn.String()
	label := "Recording"
	hint := "esc/enter to stop"
	if m.voice.dict.holdOwned {
		hint = "release to stop"
	}
	if m.voice.dict.state == dictationStopping {
		dot = sty.VoiceRecordingDotOff.String()
		label = "Finishing"
		hint = ""
	} else if !m.voice.pulseOn {
		dot = sty.VoiceRecordingDotOff.String()
	}
	out := dot + " " + sty.VoiceRecordingLabel.Render(label)
	if hint != "" && ansi.StringWidth(out)+2+len(hint) <= width {
		out += "  " + sty.VoiceRecordingHint.Render(hint)
	}
	return ansi.Truncate(out, width, "")
}

const (
	voiceIndicatorGap      = 2
	voiceIndicatorMinChips = 8
)

func (m *UI) attachmentChipArea(width int) (offset, chipWidth int, ok bool) {
	if len(m.attachments.List()) == 0 {
		return 0, 0, false
	}
	indicator := m.renderVoiceIndicator(width)
	if indicator == "" {
		return 0, width, true
	}
	offset = ansi.StringWidth(indicator) + voiceIndicatorGap
	chipWidth = width - offset
	if chipWidth < voiceIndicatorMinChips {
		return 0, 0, false
	}
	return offset, chipWidth, true
}

func (m *UI) joinVoiceIndicatorRow(width int) string {
	indicator := m.renderVoiceIndicator(width)
	offset, chipWidth, chips := m.attachmentChipArea(width)
	switch {
	case !chips:
		return indicator
	case indicator == "":
		return m.attachments.Render(chipWidth)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, indicator, strings.Repeat(" ", offset-ansi.StringWidth(indicator)), m.attachments.Render(chipWidth))
}
