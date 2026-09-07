package voice

import (
	"strconv"
	"strings"
)

// InputDevice is a microphone the capture backend can open.
type InputDevice struct {
	// ID is the stable, platform-specific identifier persisted in config
	// and passed to [SpawnPCMCapture]. macOS uses the CoreAudio device
	// UID, Linux a `pulse:<source>` / `alsa:<pcm>` name, Windows the
	// waveIn product name.
	ID string
	// Name is the human-readable label shown in the picker.
	Name string
	// Default marks the device the OS currently routes input to.
	Default bool
}

// ListInputDevices enumerates the microphones available to the capture
// backend for the current platform. The system default is flagged with
// [InputDevice.Default]; callers usually prepend a synthetic "System
// Default" entry with an empty ID.
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

// deviceUnavailableErr is the error returned when a configured input
// device cannot be opened. Callers may append backend detail to Msg.
func deviceUnavailableErr(deviceID string) *Error {
	return captureErr("input device " + strconv.Quote(deviceID) + " is unavailable (pick another with Ctrl+P → Select Microphone)")
}
