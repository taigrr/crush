//go:build windows

package voice

import (
	"fmt"
	"sync/atomic"
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
	stop    *atomic.Bool
	hwi     uintptr
	headers []waveHdr
	bufs    [][]byte
}

func (h *windowsHandle) Stop() {
	if h == nil {
		return
	}
	h.stop.Store(true)
	procWaveInStop.Call(h.hwi)
	procWaveInReset.Call(h.hwi)
	for i := range h.headers {
		procWaveInUnprep.Call(h.hwi, uintptr(unsafe.Pointer(&h.headers[i])), unsafe.Sizeof(h.headers[i]))
	}
	procWaveInClose.Call(h.hwi)
}

func spawnPCMCapture(sampleRate uint32, pcmCh chan<- []byte) (CaptureHandle, error) {
	format := waveFormatEx{
		FormatTag:      waveFormatPCM,
		Channels:       1,
		SamplesPerSec:  sampleRate,
		BitsPerSample:  16,
		BlockAlign:     2,
		AvgBytesPerSec: sampleRate * 2,
	}
	stop := &atomic.Bool{}
	handle := &windowsHandle{
		stop:    stop,
		headers: make([]waveHdr, winBufferCount),
		bufs:    make([][]byte, winBufferCount),
	}
	cb := syscall.NewCallback(func(_, uMsg, _, dwParam1, _ uintptr) uintptr {
		if stop.Load() {
			return 0
		}
		if uMsg == wimData {
			hdr := (*waveHdr)(unsafe.Pointer(dwParam1))
			if hdr != nil && hdr.BytesRecorded > 0 {
				n := int(hdr.BytesRecorded)
				chunk := unsafe.Slice(hdr.Data, n)
				out := make([]byte, n)
				copy(out, chunk)
				trySendPCM(pcmCh, out, nil)
				procWaveInAdd.Call(handle.hwi, uintptr(unsafe.Pointer(hdr)), unsafe.Sizeof(*hdr))
			}
		}
		return 0
	})
	r, _, err := procWaveInOpen.Call(
		uintptr(unsafe.Pointer(&handle.hwi)),
		uintptr(waveMapper),
		uintptr(unsafe.Pointer(&format)),
		cb,
		0,
		callbackFunction,
	)
	if r != 0 {
		return nil, captureErr(fmt.Sprintf("waveInOpen: %v", err))
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
		return nil, captureErr(fmt.Sprintf("waveInStart: %v", err))
	}
	return handle, nil
}
