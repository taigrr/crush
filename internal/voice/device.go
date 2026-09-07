package voice

import (
	"strconv"
	"strings"
)

type InputDevice struct {
	// ID is a CoreAudio UID, `pulse:<source>` / `alsa:<pcm>`, or a waveIn
	// product name depending on the platform.
	ID      string
	Name    string
	Default bool
}

func ListInputDevices() ([]InputDevice, error) {
	devices, err := listInputDevices()
	if err != nil {
		return nil, err
	}
	out := make([]InputDevice, 0, len(devices))
	for _, d := range devices {
		if strings.TrimSpace(d.ID) == "" {
			continue
		}
		if strings.TrimSpace(d.Name) == "" {
			d.Name = d.ID
		}
		out = append(out, d)
	}
	return out, nil
}

func deviceUnavailableErr(deviceID string) *Error {
	return captureErr("input device " + strconv.Quote(deviceID) + " is unavailable (pick another with Ctrl+P → Select Microphone)")
}
