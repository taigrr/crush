//go:build linux

package voice

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const deviceListTimeout = 3 * time.Second

type linuxBackend int

const (
	linuxBackendDefault linuxBackend = iota
	linuxBackendPulse
	linuxBackendALSA
)

const (
	linuxPulsePrefix = "pulse:"
	linuxALSAPrefix  = "alsa:"
)

type linuxDevice struct {
	backend linuxBackend
	name    string
}

func parseLinuxDeviceID(id string) (linuxDevice, error) {
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return linuxDevice{}, nil
	case strings.HasPrefix(id, linuxPulsePrefix):
		return linuxDevice{backend: linuxBackendPulse, name: strings.TrimPrefix(id, linuxPulsePrefix)}, nil
	case strings.HasPrefix(id, linuxALSAPrefix):
		return linuxDevice{backend: linuxBackendALSA, name: strings.TrimPrefix(id, linuxALSAPrefix)}, nil
	default:
		return linuxDevice{}, captureErr("unrecognized input device id " + strconv.Quote(id) + "; expected pulse:<source> or alsa:<pcm>")
	}
}

func listInputDevices() ([]InputDevice, error) {
	var out []InputDevice
	havePactl, haveArecord := binaryOnPath("pactl"), binaryOnPath("arecord")
	if havePactl {
		sources, err := pactlSources()
		if err != nil {
			return nil, err
		}
		out = append(out, sources...)
	}
	if haveArecord {
		if raw, err := runForOutput("arecord", "-L"); err == nil {
			out = append(out, parseArecordPCMs(string(raw))...)
		}
	}
	if !havePactl && !haveArecord {
		return nil, captureErr("no audio tools found on PATH: install pulseaudio-utils (pactl) or alsa-utils (arecord) to list microphones")
	}
	return out, nil
}

func runForOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), deviceListTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Output()
}

// pactl's text output is gettext-localised; the JSON format is not.
type pactlSource struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	MonitorSource string `json:"monitor_source"`
	Properties    struct {
		DeviceClass string `json:"device.class"`
	} `json:"properties"`
}

type pactlInfo struct {
	DefaultSourceName string `json:"default_source_name"`
}

func pactlSources() ([]InputDevice, error) {
	raw, err := runForOutput("pactl", "--format=json", "list", "sources")
	if err != nil {
		return nil, captureErr("pactl list sources: " + err.Error() + " (pactl 16 or newer is required for --format=json)")
	}
	var defaultName string
	if info, err := runForOutput("pactl", "--format=json", "info"); err == nil {
		var pi pactlInfo
		if json.Unmarshal(info, &pi) == nil {
			defaultName = pi.DefaultSourceName
		}
	}
	return parsePactlSourcesJSON(raw, defaultName)
}

func parsePactlSourcesJSON(raw []byte, defaultName string) ([]InputDevice, error) {
	var sources []pactlSource
	if err := json.Unmarshal(raw, &sources); err != nil {
		return nil, captureErr("pactl list sources: unexpected output: " + err.Error())
	}
	out := make([]InputDevice, 0, len(sources))
	for _, src := range sources {
		if src.Name == "" || src.Properties.DeviceClass == "monitor" || strings.HasSuffix(src.Name, ".monitor") {
			continue
		}
		out = append(out, InputDevice{
			ID:      linuxPulsePrefix + src.Name,
			Name:    src.Description,
			Default: defaultName != "" && src.Name == defaultName,
		})
	}
	return out, nil
}

// `arecord -L`: unindented lines are PCM names, the indented line after
// each is its description.
func parseArecordPCMs(raw string) []InputDevice {
	var out []InputDevice
	var cur *InputDevice
	flush := func() {
		if cur != nil {
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(raw, "\n") {
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if cur != nil && cur.Name == "" {
				cur.Name = strings.TrimSpace(line)
			}
			continue
		}
		flush()
		name := strings.TrimSpace(line)
		if !isCapturePCM(name) {
			continue
		}
		cur = &InputDevice{ID: linuxALSAPrefix + name}
	}
	flush()
	for i := range out {
		if out[i].Name != "" {
			out[i].Name += " (" + strings.TrimPrefix(out[i].ID, linuxALSAPrefix) + ")"
		}
	}
	return out
}

func isCapturePCM(name string) bool {
	for _, prefix := range []string{"hw:", "plughw:", "sysdefault:", "default:"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
