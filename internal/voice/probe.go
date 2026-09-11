package voice

import "runtime"

func MicFixHelp() string {
	if runtime.GOOS == "darwin" {
		return "Allow microphone access for your terminal in System Settings → Privacy & Security → Microphone, then restart the terminal. If access is already on, check the input device and level in System Settings → Sound → Input."
	}
	return "Check the default input device and its volume in your sound settings (e.g. `pavucontrol`, or `wpctl status` on PipeWire)."
}
