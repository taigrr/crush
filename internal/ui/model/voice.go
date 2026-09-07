package model

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/voice"
)

type voiceState int

const (
	voiceIdle voiceState = iota
	voiceListening
	voiceStopping
)

type voiceSession struct {
	state     voiceState
	interim   string
	holdOwned bool
	cmdCh     chan voice.Command
	cancel    context.CancelFunc
	events    chan voice.Event
}

func newVoiceSession() *voiceSession {
	return &voiceSession{
		events: make(chan voice.Event, 32),
	}
}

func (s *voiceSession) listening() bool {
	return s != nil && s.state == voiceListening
}

func (s *voiceSession) holdOwnedNow() bool {
	return s != nil && s.holdOwned && (s.state == voiceListening || s.state == voiceStopping)
}

func (s *voiceSession) reset() {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.cmdCh != nil {
		select {
		case s.cmdCh <- voice.CmdShutdown:
		default:
		}
		s.cmdCh = nil
	}
	s.state = voiceIdle
	s.interim = ""
	s.holdOwned = false
}

type voiceEventMsg voice.Event

func (m *UI) voiceEnabled() bool {
	cfg := m.com.Config()
	return cfg != nil && !cfg.VoiceDisabled()
}

func (m *UI) voiceKeybindEnabled() bool {
	cfg := m.com.Config()
	if cfg == nil {
		return true
	}
	return cfg.VoiceKeybindEnabled()
}

func (m *UI) voiceHoldMode() bool {
	cfg := m.com.Config()
	return cfg != nil && cfg.VoiceCaptureMode() == "hold"
}

func (m *UI) voiceReleasesReported() bool {
	return m.keyenh.SupportsEventTypes()
}

func (m *UI) voiceConfig() voice.Config {
	cfg := voice.DefaultConfig()
	app := m.com.Config()
	if app != nil && app.Options != nil && app.Options.Voice != nil {
		v := app.Options.Voice
		if v.APIBase != "" {
			cfg.APIBase = v.APIBase
		}
		if v.STTWSPath != "" {
			cfg.STTWSPath = v.STTWSPath
		}
		if v.Language != "" {
			cfg.Language = v.Language
		}
		if v.APIKey != "" {
			cfg.APIKey = v.APIKey
		}
	}
	if app != nil && app.Options != nil && app.Options.TUI != nil {
		if app.Options.TUI.VoiceCaptureMode == "hold" {
			cfg.CaptureMode = voice.CaptureHold
		}
		if app.Options.TUI.VoiceKeybindEnabled != nil {
			cfg.KeybindEnabled = *app.Options.TUI.VoiceKeybindEnabled
		}
	}
	cfg.APIBase = voice.ResolveAPIBase(app, cfg)
	return cfg.Normalize()
}

func (m *UI) ensureVoicePipeline() {
	if m.voice.cmdCh != nil {
		return
	}
	cmdCh := make(chan voice.Command, 8)
	eventCh := make(chan voice.Event, 32)
	ctx, cancel := context.WithCancel(context.Background())
	m.voice.cmdCh = cmdCh
	m.voice.cancel = cancel
	cfg := m.voiceConfig()
	go voice.RunPipeline(ctx, cfg, func() (string, error) {
		return voice.ResolveBearer(m.com.Config(), cfg)
	}, cmdCh, eventCh)
	go func() {
		for ev := range eventCh {
			select {
			case m.voice.events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (m *UI) waitVoiceEvent() tea.Cmd {
	ch := m.voice.events
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return voiceEventMsg(ev)
	}
}

func (m *UI) sendVoiceCmd(cmd voice.Command) {
	if m.voice.cmdCh == nil {
		return
	}
	select {
	case m.voice.cmdCh <- cmd:
	default:
	}
}

func (m *UI) beginVoiceRecording(fromHold bool) tea.Cmd {
	m.ensureVoicePipeline()
	m.voice.state = voiceListening
	m.voice.holdOwned = fromHold
	m.voice.interim = ""
	m.sendVoiceCmd(voice.CmdPress)
	return m.waitVoiceEvent()
}

func (m *UI) stopVoiceKeepingFinal() tea.Cmd {
	m.sendVoiceCmd(voice.CmdRelease)
	m.voice.state = voiceStopping
	m.voice.holdOwned = false
	return nil
}

func (m *UI) toggleVoice(fromHold bool) tea.Cmd {
	if !m.voiceEnabled() {
		return nil
	}
	if m.voice.listening() {
		return m.stopVoiceKeepingFinal()
	}
	return m.beginVoiceRecording(fromHold)
}

func (m *UI) holdReleaseVoice() tea.Cmd {
	if m.voice == nil || !m.voice.holdOwned {
		return nil
	}
	if m.voice.listening() {
		return m.stopVoiceKeepingFinal()
	}
	m.voice.reset()
	return nil
}

func (m *UI) commitVoiceInterim() string {
	if m.voice == nil {
		return ""
	}
	interim := strings.TrimSpace(m.voice.interim)
	if interim == "" {
		return ""
	}
	m.appendVoiceText(interim)
	m.voice.interim = ""
	return interim
}

func (m *UI) appendVoiceText(text string) {
	existing := m.textarea.Value()
	cursorAtEnd := m.textarea.Line() >= m.textarea.LineCount()-1
	combined := voice.CombinePromptWithVoiceText(existing, text)
	prevHeight := m.textarea.Height()
	m.textarea.SetValue(combined)
	if strings.TrimSpace(existing) == "" || cursorAtEnd {
		m.textarea.MoveToEnd()
	}
	_ = m.handleTextareaHeightChange(prevHeight)
}

func (m *UI) handleVoiceEvent(ev voice.Event) tea.Cmd {
	var cmds []tea.Cmd
	switch ev.Kind {
	case voice.EventInterim:
		if m.voice.listening() {
			m.voice.interim = ev.Text
		}
	case voice.EventFinal:
		m.voice.interim = ""
		if strings.TrimSpace(ev.Text) != "" {
			m.appendVoiceText(strings.TrimSpace(ev.Text))
		}
		if m.voice.state == voiceStopping {
			m.voice.state = voiceIdle
			m.voice.holdOwned = false
		}
	case voice.EventError:
		m.voice.reset()
		msg := "Voice: " + ev.Message
		if ev.Hint != "" {
			msg += ". " + ev.Hint
		}
		cmds = append(cmds, util.ReportWarn(msg))
	}
	if m.voice.cmdCh != nil {
		cmds = append(cmds, m.waitVoiceEvent())
	}
	return tea.Batch(cmds...)
}

func isVoiceChordPress(msg tea.KeyPressMsg) bool {
	s := msg.String()
	return s == "ctrl+space" || s == "f8"
}

func isVoiceChordRelease(msg tea.KeyReleaseMsg) bool {
	s := msg.String()
	return s == " " || s == "space" || s == "ctrl+space" || s == "f8"
}

func (m *UI) handleVoiceKeyPress(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if m.dialog.HasDialogs() {
		return nil, false
	}
	if !m.voiceEnabled() {
		return nil, false
	}
	if !isVoiceChordPress(msg) {
		return nil, false
	}
	if !m.voice.holdOwnedNow() && !m.voiceKeybindEnabled() {
		return nil, false
	}
	if msg.IsRepeat {
		return nil, true
	}
	hold := m.voiceHoldMode() && m.voiceReleasesReported()
	if hold {
		if !m.voice.listening() {
			return m.beginVoiceRecording(true), true
		}
		if !m.voice.holdOwned {
			return m.toggleVoice(false), true
		}
		return nil, true
	}
	return m.toggleVoice(false), true
}

func (m *UI) handleVoiceKeyRelease(msg tea.KeyReleaseMsg) (tea.Cmd, bool) {
	if !m.voice.holdOwnedNow() {
		return nil, false
	}
	if !isVoiceChordRelease(msg) {
		return nil, false
	}
	return m.holdReleaseVoice(), true
}

func wrapVoiceInterim(text string, maxW, maxRows int) []string {
	if maxW <= 0 || maxRows <= 0 {
		return nil
	}
	var lines []string
	var cur string
	curW := 0
	flush := func() {
		if cur == "" {
			return
		}
		lines = append(lines, cur)
		cur = ""
		curW = 0
	}
	for _, word := range strings.Fields(text) {
		ww := ansi.StringWidth(word)
		if cur != "" && curW+1+ww > maxW {
			flush()
			if len(lines) >= maxRows {
				break
			}
		}
		if cur == "" {
			if ww > maxW {
				cur = ansi.Truncate(word, maxW, "…")
				curW = ansi.StringWidth(cur)
			} else {
				cur = word
				curW = ww
			}
		} else {
			cur += " " + word
			curW += 1 + ww
		}
	}
	if cur != "" && len(lines) < maxRows {
		lines = append(lines, cur)
	}
	if len(lines) > maxRows {
		lines = lines[:maxRows]
	}
	return lines
}

func (m *UI) overlayVoiceInterim(view string, width int) string {
	if m.voice == nil {
		return view
	}
	interim := strings.TrimSpace(m.voice.interim)
	if !m.voice.listening() && interim == "" {
		return view
	}
	if interim == "" {
		return view
	}
	sty := m.com.Styles.Editor.VoiceInterim
	committed := m.textarea.Value()
	if strings.TrimSpace(committed) == "" {
		height := max(1, m.textarea.Height())
		body := wrapVoiceInterim(interim, max(1, width-4), height)
		rendered := make([]string, len(body))
		for i, line := range body {
			rendered[i] = sty.Render(line)
		}
		return overlayEditorLines(view, rendered)
	}
	return appendGhostSuffix(view, sty.Render(" "+interim), width)
}

func overlayEditorLines(view string, overlay []string) string {
	lines := strings.Split(view, "\n")
	if len(overlay) == 0 || len(lines) == 0 {
		return view
	}
	end := len(lines)
	if end > 0 && lines[end-1] == "" {
		end--
	}
	n := min(len(overlay), end)
	for i := 0; i < n; i++ {
		lines[i] = overlay[i]
	}
	return strings.Join(lines, "\n")
}

func appendGhostSuffix(view, suffix string, width int) string {
	lines := strings.Split(view, "\n")
	idx := len(lines) - 1
	for idx >= 0 && strings.TrimSpace(ansi.Strip(lines[idx])) == "" {
		idx--
	}
	if idx < 0 {
		return view
	}
	avail := width - ansi.StringWidth(ansi.Strip(lines[idx]))
	if avail <= 1 {
		return view
	}
	lines[idx] = lines[idx] + ansi.Truncate(suffix, avail, "…")
	return strings.Join(lines, "\n")
}
