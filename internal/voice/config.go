package voice

import (
	"fmt"
	"strings"
)

const DefaultSampleRate uint32 = 16_000

const (
	defaultAPIBase     = "https://api.x.ai"
	defaultSTTWSPath   = "/v1/stt"
	defaultEndpointing = uint32(400)
)

type Config struct {
	APIBase          string
	STTWSPath        string
	Language         string
	SampleRate       uint32
	EndpointingMS    uint32
	InterimResults   bool
	APIKey           string
	InputDevice      string
	ClientIdentifier string
	UserAgent        string
}

func DefaultConfig() Config {
	return Config{
		APIBase:          defaultAPIBase,
		STTWSPath:        defaultSTTWSPath,
		Language:         LanguageDefault,
		SampleRate:       DefaultSampleRate,
		EndpointingMS:    defaultEndpointing,
		InterimResults:   true,
		ClientIdentifier: "crush",
		UserAgent:        "crush",
	}
}

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
	if c.ClientIdentifier == "" {
		c.ClientIdentifier = d.ClientIdentifier
	}
	if c.UserAgent == "" {
		c.UserAgent = d.UserAgent
	}
	return c
}

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
