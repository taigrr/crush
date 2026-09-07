//go:build windows

package voice

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

const (
	waveMapper       = 0xffffffff
	waveFormatPCM    = 1
	callbackFunction = 0x00030000
	wimData          = 0x3C0
	winBufferCount   = 4
	winBufferBytes   = 4096
)

var (
	winmm             = syscall.NewLazyDLL("winmm.dll")
	procWaveInOpen    = winmm.NewProc("waveInOpen")
	procWaveInPrepare = winmm.NewProc("waveInPrepareHeader")
	procWaveInAdd     = winmm.NewProc("waveInAddBuffer")
	procWaveInStart   = winmm.NewProc("waveInStart")
	procWaveInStop    = winmm.NewProc("waveInStop")
	procWaveInReset   = winmm.NewProc("waveInReset")
	procWaveInUnprep  = winmm.NewProc("waveInUnprepareHeader")
	procWaveInClose   = winmm.NewProc("waveInClose")
	procWaveInNumDevs = winmm.NewProc("waveInGetNumDevs")
	procWaveInDevCaps = winmm.NewProc("waveInGetDevCapsW")
)

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	ExtraSize      uint16
}

type waveHdr struct {
	Data          *byte
	BufferLength  uint32
	BytesRecorded uint32
	User          uintptr
	Flags         uint32
	Loops         uint32
	Next          *waveHdr
	Reserved      uintptr
}

type windowsHandle struct {
	once    sync.Once
	hwi     uintptr
	headers []waveHdr
	bufs    [][]byte
	sink    uintptr
	stream  *pcmStream
}

func (h *windowsHandle) Stop() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		procWaveInStop.Call(h.hwi)
		procWaveInReset.Call(h.hwi)
		for i := range h.headers {
			procWaveInUnprep.Call(h.hwi, uintptr(unsafe.Pointer(&h.headers[i])), unsafe.Sizeof(h.headers[i]))
		}
		procWaveInClose.Call(h.hwi)
		unregisterCaptureSink(h.sink)
		h.stream.close()
	})
}

// waveInCallback is the single waveIn trampoline shared by all captures;
// the sink id travels in dwInstance. dwParam1 stays a raw uintptr because
// WIM_OPEN/WIM_CLOSE deliver a non-pointer value in that slot.
var waveInCallback = syscall.NewCallback(func(hwi, uMsg, dwInstance, dwParam1, _ uintptr) uintptr {
	if uMsg != wimData {
		return 0
	}
	stream, ok := lookupCaptureSink(dwInstance)
	if !ok {
		return 0
	}
	// go vet's unsafeptr check flags this uintptr→pointer conversion; it
	// is the documented Win32 contract for WIM_DATA and cannot be typed
	// in the callback signature (see comment above).
	hdr := (*waveHdr)(unsafe.Pointer(dwParam1))
	if hdr != nil && hdr.BytesRecorded > 0 {
		n := int(hdr.BytesRecorded)
		chunk := unsafe.Slice(hdr.Data, n)
		out := make([]byte, n)
		copy(out, chunk)
		stream.push(out)
		procWaveInAdd.Call(hwi, dwParam1, unsafe.Sizeof(*hdr))
	}
	return 0
})

func spawnPCMCapture(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
	deviceIndex := uintptr(waveMapper)
	if deviceID != "" {
		idx, ok := findWaveInDevice(deviceID)
		if !ok {
			return nil, nil, deviceUnavailableErr(deviceID)
		}
		deviceIndex = idx
	}
	format := waveFormatEx{
		FormatTag:      waveFormatPCM,
		Channels:       1,
		SamplesPerSec:  sampleRate,
		BitsPerSample:  16,
		BlockAlign:     2,
		AvgBytesPerSec: sampleRate * 2,
	}
	stream := newPCMStream(captureBuffer)
	sink := registerCaptureSink(stream)
	handle := &windowsHandle{
		headers: make([]waveHdr, winBufferCount),
		bufs:    make([][]byte, winBufferCount),
		sink:    sink,
		stream:  stream,
	}
	r, _, err := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&handle.hwi)),
		deviceIndex,
		uintptr(unsafe.Pointer(&format)),
		waveInCallback,
		sink,
		callbackFunction,
	)
	if r != 0 {
		unregisterCaptureSink(sink)
		stream.close()
		if deviceID != "" {
			return nil, nil, deviceUnavailableErr(deviceID)
		}
		return nil, nil, captureErr(fmt.Sprintf("waveInOpen: %v", err))
	}
	for i := range handle.headers {
		handle.bufs[i] = make([]byte, winBufferBytes)
		handle.headers[i].Data = &handle.bufs[i][0]
		handle.headers[i].BufferLength = uint32(len(handle.bufs[i]))
		procWaveInPrepare.Call(handle.hwi, uintptr(unsafe.Pointer(&handle.headers[i])), unsafe.Sizeof(handle.headers[i]))
		procWaveInAdd.Call(handle.hwi, uintptr(unsafe.Pointer(&handle.headers[i])), unsafe.Sizeof(handle.headers[i]))
	}
	if r, _, err := procWaveInStart.Call(handle.hwi); r != 0 {
		handle.Stop()
		return nil, nil, captureErr(fmt.Sprintf("waveInStart: %v", err))
	}
	return handle, stream.C(), nil
}
