package voice

import "runtime"

// MicFixHelp is platform-specific fix text for a mic that isn't being
// picked up. On macOS the grant is for the terminal app and only
// applies after that app restarts.
func MicFixHelp() string {
	switch runtime.GOOS {
	case "darwin":
		return "Allow microphone access for your terminal in System Settings → Privacy & Security → Microphone, then restart the terminal. If access is already on, check the input device and level in System Settings → Sound → Input."
	case "windows":
		return "Allow microphone access in Settings → Privacy & security → Microphone, and check the input device and level in Settings → System → Sound."
	default:
		return "Check the default input device and its volume in your sound settings (e.g. `pavucontrol`, or `wpctl status` on PipeWire)."
	}
}
