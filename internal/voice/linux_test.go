//go:build linux

package voice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePactlSourcesJSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`[
  {"name":"alsa_output.pci.analog-stereo.monitor","description":"Monitor","properties":{"device.class":"monitor"}},
  {"name":"alsa_input.pci.analog-stereo","description":"Eingebautes Tongerät","properties":{"device.class":"sound"}},
  {"name":"alsa_input.usb-Blue_Yeti.analog-stereo","description":"Yeti","properties":{"device.class":"sound"}}
]`)
	got, err := parsePactlSourcesJSON(raw, "alsa_input.usb-Blue_Yeti.analog-stereo")
	require.NoError(t, err)
	require.Equal(t, []InputDevice{
		{ID: "pulse:alsa_input.pci.analog-stereo", Name: "Eingebautes Tongerät"},
		{ID: "pulse:alsa_input.usb-Blue_Yeti.analog-stereo", Name: "Yeti", Default: true},
	}, got)
	_, err = parsePactlSourcesJSON([]byte("Source #0\n\tName: x"), "")
	require.Error(t, err, "text output from an old pactl is rejected, not misparsed")
}

func TestParseArecordPCMs(t *testing.T) {
	t.Parallel()
	raw := `null
    Discard all samples
default
    Playback/recording through PulseAudio
sysdefault:CARD=PCH
    HDA Intel PCH, ALC295 Analog
    Default Audio Device
hw:CARD=Yeti,DEV=0
    Yeti Stereo Microphone, USB Audio
    Direct hardware device
`
	require.Equal(t, []InputDevice{
		{ID: "alsa:sysdefault:CARD=PCH", Name: "HDA Intel PCH, ALC295 Analog (sysdefault:CARD=PCH)"},
		{ID: "alsa:hw:CARD=Yeti,DEV=0", Name: "Yeti Stereo Microphone, USB Audio (hw:CARD=Yeti,DEV=0)"},
	}, parseArecordPCMs(raw))
}
