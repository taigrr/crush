package model

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/ui/util"
	"github.com/taigrr/crush/internal/voice"
)

// voiceSession is the TUI's handle on the capture pipeline: the command
// and event channels, the single outstanding event reader, and the
// recording-indicator pulse. Turn bookkeeping and prompt edits live in
// [dictation].
type voiceSession struct {
	dict *dictation

	cmdCh  chan voice.Command
	cancel context.CancelFunc
	events chan voice.Event
	// waiting is true while a waitVoiceEvent command is outstanding, so
	// exactly one reader ever drains events and ordering is preserved.
	waiting bool

	// pulseOn is the current phase of the recording-dot pulse; pulseGen
	// invalidates ticks from a previous recording.
	pulseOn  bool
	pulseGen int
}

func newVoiceSession(dict *dictation) *voiceSession {
	return &voiceSession{
		dict:   dict,
		events: make(chan voice.Event, 32),
	}
}

// reset tears down the pipeline and forgets every turn.
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
		case s.cmdCh <- voice.Shutdown():
		default:
		}
		s.cmdCh = nil
	}
	s.dict.reset()
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
		cfg.InputDevice = v.InputDevice
	}
	cfg.APIBase = voice.ResolveAPIBase(cfg)
	return cfg.Normalize()
}

// ensureVoicePipeline starts the capture pipeline on first use and
// returns the command that begins draining its events.
func (m *UI) ensureVoicePipeline() tea.Cmd {
	if m.voice.cmdCh != nil {
		return m.waitVoiceEvent()
	}
	cmdCh := make(chan voice.Command, 8)
	eventCh := make(chan voice.Event, 32)
	ctx, cancel := context.WithCancel(context.Background())
	m.voice.cmdCh = cmdCh
	m.voice.cancel = cancel
	cfg := m.voiceConfig()
	go voice.RunPipeline(ctx, cfg, voice.NewBearerFunc(m.com.Config, m.com.Workspace, cfg), cmdCh, eventCh)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-eventCh:
				select {
				case m.voice.events <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return m.waitVoiceEvent()
}

// waitVoiceEvent arms the single event reader. It returns nil when one
// is already outstanding.
func (m *UI) waitVoiceEvent() tea.Cmd {
	if m.voice.waiting {
		return nil
	}
	m.voice.waiting = true
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
	wait := m.ensureVoicePipeline()
	turn := m.voice.dict.begin(fromHold)
	m.sendVoiceCmd(voice.Press(turn))
	return tea.Batch(wait, m.startVoicePulse())
}

// stopVoiceKeepingFinal releases the current turn. Its final transcript
// (or EventStopped) still arrives and settles the phrase.
func (m *UI) stopVoiceKeepingFinal() tea.Cmd {
	m.sendVoiceCmd(voice.Release())
	m.voice.dict.release()
	return nil
}

func (m *UI) toggleVoice(fromHold bool) tea.Cmd {
	if !m.voiceEnabled() {
		return nil
	}
	if m.voice.dict.listening() {
		return m.stopVoiceKeepingFinal()
	}
	return m.beginVoiceRecording(fromHold)
}

func (m *UI) holdReleaseVoice() tea.Cmd {
	if m.voice == nil || !m.voice.dict.holdOwned {
		return nil
	}
	if m.voice.dict.listening() {
		return m.stopVoiceKeepingFinal()
	}
	m.voice.reset()
	return nil
}

// handleVoiceEvent applies a pipeline event to the dictation state and
// reconciles the editor layout for any text it inserted.
func (m *UI) handleVoiceEvent(ev voice.Event) tea.Cmd {
	m.voice.waiting = false
	var cmds []tea.Cmd
	prevHeight := m.textarea.Height()
	if failure := m.voice.dict.apply(ev); failure != nil {
		m.voice.reset()
		msg := "Voice: " + failure.Message
		if failure.Hint != "" {
			msg += ". " + failure.Hint
		}
		cmds = append(cmds, util.ReportWarn(msg))
	}
	cmds = append(cmds, m.handleTextareaHeightChange(prevHeight))
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
	if !m.voice.dict.holdOwnedNow() && !m.voiceKeybindEnabled() {
		return nil, false
	}
	if msg.IsRepeat {
		return nil, true
	}
	hold := m.voiceHoldMode() && m.voiceReleasesReported()
	if hold {
		if !m.voice.dict.listening() {
			return m.beginVoiceRecording(true), true
		}
		if !m.voice.dict.holdOwned {
			return m.toggleVoice(false), true
		}
		return nil, true
	}
	return m.toggleVoice(false), true
}

func (m *UI) handleVoiceKeyRelease(msg tea.KeyReleaseMsg) (tea.Cmd, bool) {
	if !m.voice.dict.holdOwnedNow() {
		return nil, false
	}
	if !isVoiceChordRelease(msg) {
		return nil, false
	}
	return m.holdReleaseVoice(), true
}
