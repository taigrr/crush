package voice

import (
	"fmt"
	"strings"
)

// DefaultSampleRate is the STT capture rate (Hz). Shared with the
// `__mic-capture` helper's argv default so parent and child agree when
// `--rate` is omitted.
const DefaultSampleRate uint32 = 16_000

const (
	defaultAPIBase     = "https://api.x.ai"
	defaultSTTWSPath   = "/v1/stt"
	defaultEndpointing = uint32(400)
)

// CaptureMode is how the Ctrl+Space / F8 chord behaves.
type CaptureMode string

const (
	CaptureToggle CaptureMode = "toggle"
	CaptureHold   CaptureMode = "hold"
)

// Config is the STT transport and UI settings for voice dictation.
type Config struct {
	// APIBase is the HTTPS API root (or bare host). Bases may end in
	// `/v1` or `/xai/v1`; the default STT path de-duplicates a leading
	// `v1/` so both become `…/v1/stt`.
	APIBase   string
	STTWSPath string
	// Language is a catalog code or "auto".
	Language       string
	SampleRate     uint32
	EndpointingMS  uint32
	InterimResults bool
	// KeybindEnabled, when false, silences the Ctrl+Space / F8 chord
	// without disabling `/voice`.
	KeybindEnabled bool
	// CaptureMode is "toggle" (default) or "hold". Hold requires a
	// terminal that reports key releases (Kitty protocol).
	CaptureMode CaptureMode
	// APIKey is an optional dedicated STT bearer. Empty means inherit
	// from the grok provider / XAI_API_KEY.
	APIKey string
	// ClientIdentifier and UserAgent are stamped on the handshake for
	// server-side attribution; empty omits the headers.
	ClientIdentifier string
	UserAgent        string
}

// DefaultConfig returns production defaults matching grok-build.
func DefaultConfig() Config {
	return Config{
		APIBase:          defaultAPIBase,
		STTWSPath:        defaultSTTWSPath,
		Language:         LanguageDefault,
		SampleRate:       DefaultSampleRate,
		EndpointingMS:    defaultEndpointing,
		InterimResults:   true,
		KeybindEnabled:   true,
		CaptureMode:      CaptureToggle,
		ClientIdentifier: "crush",
		UserAgent:        "crush",
	}
}

// Normalize fills empty fields with defaults and canonicalizes language
// and capture mode.
func (c Config) Normalize() Config {
	d := DefaultConfig()
	if strings.TrimSpace(c.APIBase) == "" {
		c.APIBase = d.APIBase
	} else {
		c.APIBase = strings.TrimRight(strings.TrimSpace(c.APIBase), "/")
	}
	if strings.TrimSpace(c.STTWSPath) == "" {
		c.STTWSPath = d.STTWSPath
	}
	if c.SampleRate == 0 {
		c.SampleRate = d.SampleRate
	}
	if c.EndpointingMS == 0 {
		c.EndpointingMS = d.EndpointingMS
	}
	c.Language = CanonicalizeLanguage(c.Language)
	switch CaptureMode(strings.ToLower(strings.TrimSpace(string(c.CaptureMode)))) {
	case CaptureHold:
		c.CaptureMode = CaptureHold
	default:
		c.CaptureMode = CaptureToggle
	}
	if c.ClientIdentifier == "" {
		c.ClientIdentifier = d.ClientIdentifier
	}
	if c.UserAgent == "" {
		c.UserAgent = d.UserAgent
	}
	return c
}

// STTWSURL derives the streaming STT WebSocket URL. Rejects plaintext
// `http://` / `ws://`.
func (c Config) STTWSURL() (string, error) {
	return wsURL(c.APIBase, c.STTWSPath)
}

func wsURL(apiBase, path string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	path = strings.TrimLeft(strings.TrimSpace(path), "/")
	if stripScheme(base, "http://") != "" || stripScheme(base, "ws://") != "" {
		return "", configErr(fmt.Sprintf(
			"insecure voice api_base %q: voice requires a TLS endpoint (https:// / wss://). Refusing to send the bearer token over a plaintext connection.",
			apiBase,
		))
	}
	rest := base
	if s := stripScheme(base, "https://"); s != "" {
		rest = s
	} else if s := stripScheme(base, "wss://"); s != "" {
		rest = s
	}
	if strings.HasSuffix(rest, "/v1") {
		if restPath, ok := strings.CutPrefix(path, "v1/"); ok {
			path = restPath
		}
	}
	return "wss://" + rest + "/" + path, nil
}

func stripScheme(s, scheme string) string {
	if len(s) < len(scheme) {
		return ""
	}
	if strings.EqualFold(s[:len(scheme)], scheme) {
		return s[len(scheme):]
	}
	return ""
}
