//go:build windows

package voice

import (
	"fmt"
	"syscall"
	"unsafe"
)

// waveInCapsW mirrors WAVEINCAPSW from mmeapi.h.
type waveInCapsW struct {
	Mid           uint16
	Pid           uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved1     uint16
}

// listInputDevices enumerates waveIn devices. Device indices are not
// stable across reboots or hot-plugs, so the product name is used as the
// ID and resolved back to an index at open time; a repeated name gets an
// ordinal suffix (`Name #2`) so identical devices stay distinct.
// WAVE_MAPPER picks the Windows default, so no entry is flagged Default.
func listInputDevices() ([]InputDevice, error) {
	names := waveInDeviceNames()
	devices := make([]InputDevice, 0, len(names))
	for i, name := range names {
		if name == "" {
			continue
		}
		devices = append(devices, InputDevice{ID: waveInDeviceID(names, i), Name: name})
	}
	return devices, nil
}

func waveInDeviceNames() []string {
	count, _, _ := procWaveInNumDevs.Call()
	names := make([]string, count)
	for i := range names {
		names[i], _ = waveInDeviceName(uintptr(i))
	}
	return names
}

// waveInDeviceID returns the ID for names[i]: the name itself for the
// first occurrence, `name #n` for the nth duplicate.
func waveInDeviceID(names []string, i int) string {
	dup := 0
	for j := 0; j <= i; j++ {
		if names[j] == names[i] {
			dup++
		}
	}
	if dup == 1 {
		return names[i]
	}
	return fmt.Sprintf("%s #%d", names[i], dup)
}

func waveInDeviceName(index uintptr) (string, bool) {
	var caps waveInCapsW
	r, _, _ := procWaveInDevCaps.Call(index, uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
	if r != 0 {
		return "", false
	}
	return syscall.UTF16ToString(caps.Pname[:]), true
}

// findWaveInDevice resolves a device ID back to its current waveIn index.
func findWaveInDevice(id string) (uintptr, bool) {
	names := waveInDeviceNames()
	for i := range names {
		if names[i] != "" && waveInDeviceID(names, i) == id {
			return uintptr(i), true
		}
	}
	return 0, false
}
