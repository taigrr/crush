//go:build windows

package voice

import (
	"fmt"
	"syscall"
	"unsafe"
)

type waveInCapsW struct {
	Mid           uint16
	Pid           uint16
	DriverVersion uint32
	Pname         [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved1     uint16
}

// waveIn indices are not stable across hot-plugs, so the product name is
// the ID (`Name #2` for duplicates) and is resolved to an index at open.
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

func findWaveInDevice(id string) (uintptr, bool) {
	names := waveInDeviceNames()
	for i := range names {
		if names[i] != "" && waveInDeviceID(names, i) == id {
			return uintptr(i), true
		}
	}
	return 0, false
}
