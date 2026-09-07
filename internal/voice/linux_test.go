//go:build linux

package voice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateRecordersOrder(t *testing.T) {
	t.Parallel()
	available := func(name string) bool {
		return name == "pw-record" || name == "parec" || name == "arecord"
	}
	got := candidateRecorders(available, func() bool { return true })
	require.Equal(t, []recorder{recPwRecord, recParec, recArecord}, got)

	got = candidateRecorders(available, func() bool { return false })
	require.Equal(t, []recorder{recParec, recArecord, recPwRecord}, got)
}

func TestParseLinuxDeviceID(t *testing.T) {
	t.Parallel()
	d, err := parseLinuxDeviceID("")
	require.NoError(t, err)
	require.Equal(t, linuxDevice{}, d)
	d, err = parseLinuxDeviceID("pulse:alsa_input.usb-Blue_Yeti.analog-stereo")
	require.NoError(t, err)
	require.Equal(t, linuxDevice{backend: linuxBackendPulse, name: "alsa_input.usb-Blue_Yeti.analog-stereo"}, d)
	d, err = parseLinuxDeviceID("alsa:hw:1,0")
	require.NoError(t, err)
	require.Equal(t, linuxDevice{backend: linuxBackendALSA, name: "hw:1,0"}, d)
	_, err = parseLinuxDeviceID("bogus")
	require.Error(t, err)
}

func TestRecorderArgsAndAccepts(t *testing.T) {
	t.Parallel()
	pulse := linuxDevice{backend: linuxBackendPulse, name: "src"}
	alsa := linuxDevice{backend: linuxBackendALSA, name: "hw:1,0"}
	require.True(t, recPwRecord.accepts(pulse))
	require.True(t, recParec.accepts(pulse))
	require.False(t, recArecord.accepts(pulse))
	require.True(t, recArecord.accepts(alsa))
	require.False(t, recPwRecord.accepts(alsa))
	require.True(t, recArecord.accepts(linuxDevice{}))

	require.Equal(t, []string{"--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "--target", "src", "-"}, recPwRecord.args(16000, pulse))
	require.Equal(t, []string{"--raw", "--format=s16le", "--rate=16000", "--channels=1", "--device=src"}, recParec.args(16000, pulse))
	require.Equal(t, []string{"-q", "-t", "raw", "-f", "S16_LE", "-c", "1", "-r", "16000", "-D", "hw:1,0", "-"}, recArecord.args(16000, alsa))
	require.Equal(t, []string{"--raw", "--rate", "16000", "--channels", "1", "--format", "s16", "-"}, recPwRecord.args(16000, linuxDevice{}))
}

func TestParsePactlSourcesJSON(t *testing.T) {
	t.Parallel()
	raw := []byte(`[
  {"index":0,"state":"SUSPENDED","name":"alsa_output.pci-0000_00_1f.3.analog-stereo.monitor",
   "description":"Monitor of Built-in Audio Analog Stereo","monitor_source":"",
   "properties":{"device.class":"monitor"}},
  {"index":1,"state":"RUNNING","name":"alsa_input.pci-0000_00_1f.3.analog-stereo",
   "description":"Eingebautes Tongerät Analog Stereo","monitor_source":"",
   "properties":{"device.class":"sound","alsa.card":"0"}},
  {"index":2,"state":"IDLE","name":"alsa_input.usb-Blue_Yeti-00.analog-stereo",
   "description":"Yeti Stereo Microphone","monitor_source":"",
   "properties":{"device.class":"sound"}}
]`)
	got, err := parsePactlSourcesJSON(raw, "alsa_input.usb-Blue_Yeti-00.analog-stereo")
	require.NoError(t, err)
	require.Equal(t, []InputDevice{
		{ID: "pulse:alsa_input.pci-0000_00_1f.3.analog-stereo", Name: "Eingebautes Tongerät Analog Stereo"},
		{ID: "pulse:alsa_input.usb-Blue_Yeti-00.analog-stereo", Name: "Yeti Stereo Microphone", Default: true},
	}, got, "monitors are skipped and localised descriptions pass through untouched")

	_, err = parsePactlSourcesJSON([]byte("Source #0\n\tName: x"), "")
	require.Error(t, err, "text output from an old pactl is rejected, not misparsed")
}

func TestParseArecordPCMs(t *testing.T) {
	t.Parallel()
	raw := `null
    Discard all samples (playback) or generate zero samples (capture)
default
    Playback/recording through the PulseAudio sound server
sysdefault:CARD=PCH
    HDA Intel PCH, ALC295 Analog
    Default Audio Device
front:CARD=PCH,DEV=0
    HDA Intel PCH, ALC295 Analog
    Front output / input
hw:CARD=Yeti,DEV=0
    Yeti Stereo Microphone, USB Audio
    Direct hardware device without any conversions
plughw:CARD=Yeti,DEV=0
    Yeti Stereo Microphone, USB Audio
    Hardware device with all software conversions
`
	got := parseArecordPCMs(raw)
	require.Equal(t, []InputDevice{
		{ID: "alsa:sysdefault:CARD=PCH", Name: "HDA Intel PCH, ALC295 Analog (sysdefault:CARD=PCH)"},
		{ID: "alsa:hw:CARD=Yeti,DEV=0", Name: "Yeti Stereo Microphone, USB Audio (hw:CARD=Yeti,DEV=0)"},
		{ID: "alsa:plughw:CARD=Yeti,DEV=0", Name: "Yeti Stereo Microphone, USB Audio (plughw:CARD=Yeti,DEV=0)"},
	}, got)
}
