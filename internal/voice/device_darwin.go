//go:build darwin

package voice

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	kAudioObjectSystemObject                 = 1
	kAudioObjectPropertyScopeGlobal          = 0x676C6F62 // 'glob'
	kAudioObjectPropertyScopeInput           = 0x696E7074 // 'inpt'
	kAudioObjectPropertyElementMain          = 0
	kAudioHardwarePropertyDevices            = 0x64657623 // 'dev#'
	kAudioHardwarePropertyDefaultInputDevice = 0x64496E20 // 'dIn '
	kAudioDevicePropertyStreams              = 0x73746D23 // 'stm#'
	kAudioDevicePropertyDeviceUID            = 0x75696420 // 'uid '
	kAudioObjectPropertyName                 = 0x6C6E616D // 'lnam'

	kCFStringEncodingUTF8 = 0x08000100
	cfStringBufSize       = 512
)

type audioObjectPropertyAddress struct {
	mSelector uint32
	mScope    uint32
	mElement  uint32
}

var (
	coreAudioOnce                  sync.Once
	coreAudioInitErr               error
	audioObjectGetPropertyDataSize func(inObjectID uint32, inAddress *audioObjectPropertyAddress, inQualifierDataSize uint32, inQualifierData unsafe.Pointer, outDataSize *uint32) int32
	audioObjectGetPropertyData     func(inObjectID uint32, inAddress *audioObjectPropertyAddress, inQualifierDataSize uint32, inQualifierData unsafe.Pointer, ioDataSize *uint32, outData unsafe.Pointer) int32
	cfStringGetCString             func(theString uintptr, buffer *byte, bufferSize int, encoding uint32) uint8
	cfStringCreateWithCString      func(alloc uintptr, cStr *byte, encoding uint32) uintptr
	cfRelease                      func(cf uintptr)
)

func initCoreAudio() error {
	coreAudioOnce.Do(func() {
		coreAudio, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			coreAudioInitErr = captureErr("CoreAudio: " + err.Error())
			return
		}
		coreFoundation, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			coreAudioInitErr = captureErr("CoreFoundation: " + err.Error())
			return
		}
		purego.RegisterLibFunc(&audioObjectGetPropertyDataSize, coreAudio, "AudioObjectGetPropertyDataSize")
		purego.RegisterLibFunc(&audioObjectGetPropertyData, coreAudio, "AudioObjectGetPropertyData")
		purego.RegisterLibFunc(&cfStringGetCString, coreFoundation, "CFStringGetCString")
		purego.RegisterLibFunc(&cfStringCreateWithCString, coreFoundation, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfRelease, coreFoundation, "CFRelease")
	})
	return coreAudioInitErr
}

func listInputDevices() ([]InputDevice, error) {
	if err := initCoreAudio(); err != nil {
		return nil, err
	}
	addr := audioObjectPropertyAddress{kAudioHardwarePropertyDevices, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain}
	var size uint32
	if st := audioObjectGetPropertyDataSize(kAudioObjectSystemObject, &addr, 0, nil, &size); st != 0 {
		return nil, captureErr(fmt.Sprintf("AudioObjectGetPropertyDataSize(devices) failed: %d", st))
	}
	count := int(size) / 4
	if count == 0 {
		return nil, nil
	}
	ids := make([]uint32, count)
	if st := audioObjectGetPropertyData(kAudioObjectSystemObject, &addr, 0, nil, &size, unsafe.Pointer(&ids[0])); st != 0 {
		return nil, captureErr(fmt.Sprintf("AudioObjectGetPropertyData(devices) failed: %d", st))
	}
	ids = ids[:int(size)/4]

	var defaultID uint32
	defAddr := audioObjectPropertyAddress{kAudioHardwarePropertyDefaultInputDevice, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain}
	defSize := uint32(unsafe.Sizeof(defaultID))
	_ = audioObjectGetPropertyData(kAudioObjectSystemObject, &defAddr, 0, nil, &defSize, unsafe.Pointer(&defaultID))

	devices := make([]InputDevice, 0, len(ids))
	for _, id := range ids {
		if !hasInputStreams(id) {
			continue
		}
		uid := cfStringProperty(id, kAudioDevicePropertyDeviceUID)
		if uid == "" {
			continue
		}
		devices = append(devices, InputDevice{
			ID:      uid,
			Name:    cfStringProperty(id, kAudioObjectPropertyName),
			Default: id == defaultID,
		})
	}
	return devices, nil
}

func hasInputStreams(deviceID uint32) bool {
	addr := audioObjectPropertyAddress{kAudioDevicePropertyStreams, kAudioObjectPropertyScopeInput, kAudioObjectPropertyElementMain}
	var size uint32
	if st := audioObjectGetPropertyDataSize(deviceID, &addr, 0, nil, &size); st != 0 {
		return false
	}
	return size > 0
}

func cfStringProperty(deviceID uint32, selector uint32) string {
	addr := audioObjectPropertyAddress{selector, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain}
	var cf uintptr
	size := uint32(unsafe.Sizeof(cf))
	if st := audioObjectGetPropertyData(deviceID, &addr, 0, nil, &size, unsafe.Pointer(&cf)); st != 0 || cf == 0 {
		return ""
	}
	defer cfRelease(cf)
	return cfStringToGo(cf)
}

func cfStringToGo(cf uintptr) string {
	buf := make([]byte, cfStringBufSize)
	if cfStringGetCString(cf, &buf[0], len(buf), kCFStringEncodingUTF8) == 0 {
		return ""
	}
	for i, b := range buf {
		if b == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

func cfStringCreate(s string) uintptr {
	cstr := append([]byte(s), 0)
	return cfStringCreateWithCString(0, &cstr[0], kCFStringEncodingUTF8)
}
