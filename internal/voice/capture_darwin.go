//go:build darwin

package voice

import (
	"fmt"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	kAudioFormatLinearPCM       = 0x6C70636D // 'lpcm'
	kAudioFormatFlagIsSignedInt = 1 << 2
	kAudioFormatFlagIsPacked    = 1 << 3
	darwinBufferCount           = 4
	darwinBufferFrames          = 1024
)

type audioStreamBasicDescription struct {
	mSampleRate       float64
	mFormatID         uint32
	mFormatFlags      uint32
	mBytesPerPacket   uint32
	mFramesPerPacket  uint32
	mBytesPerFrame    uint32
	mChannelsPerFrame uint32
	mBitsPerChannel   uint32
	mReserved         uint32
}

type audioQueueBuffer struct {
	mAudioDataBytesCapacity    uint32
	mAudioData                 uintptr
	mAudioDataByteSize         uint32
	mUserData                  uintptr
	mPacketDescriptionCapacity uint32
	mPacketDescriptions        uintptr
	mPacketDescriptionCount    uint32
}

type darwinHandle struct {
	stop  *atomic.Bool
	queue uintptr
}

func (h *darwinHandle) Stop() {
	if h == nil {
		return
	}
	h.stop.Store(true)
	if audioQueueStop != nil && h.queue != 0 {
		audioQueueStop(h.queue, true)
	}
	if audioQueueDispose != nil && h.queue != 0 {
		audioQueueDispose(h.queue, true)
		h.queue = 0
	}
}

var (
	audioOnce                atomic.Bool
	audioInitErr             error
	audioQueueNewInput       func(inFormat *audioStreamBasicDescription, inCallbackProc uintptr, inUserData unsafe.Pointer, inCallbackRunLoop, inCallbackRunLoopMode uintptr, inFlags uint32, outAQ *uintptr) uintptr
	audioQueueAllocateBuffer func(inAQ uintptr, inBufferByteSize uint32, outBuffer *uintptr) uintptr
	audioQueueEnqueueBuffer  func(inAQ uintptr, inBuffer uintptr, inNumPacketDescs uint32, inPacketDescs uintptr) uintptr
	audioQueueStart          func(inAQ uintptr, inStartTime uintptr) uintptr
	audioQueueStop           func(inAQ uintptr, inImmediate bool) uintptr
	audioQueueDispose        func(inAQ uintptr, inImmediate bool) uintptr
)

func initAudioToolbox() error {
	if audioOnce.Swap(true) {
		return audioInitErr
	}
	toolbox, err := purego.Dlopen("/System/Library/Frameworks/AudioToolbox.framework/AudioToolbox", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		audioInitErr = captureErr("AudioToolbox: " + err.Error())
		return audioInitErr
	}
	purego.RegisterLibFunc(&audioQueueNewInput, toolbox, "AudioQueueNewInput")
	purego.RegisterLibFunc(&audioQueueAllocateBuffer, toolbox, "AudioQueueAllocateBuffer")
	purego.RegisterLibFunc(&audioQueueEnqueueBuffer, toolbox, "AudioQueueEnqueueBuffer")
	purego.RegisterLibFunc(&audioQueueStart, toolbox, "AudioQueueStart")
	purego.RegisterLibFunc(&audioQueueStop, toolbox, "AudioQueueStop")
	purego.RegisterLibFunc(&audioQueueDispose, toolbox, "AudioQueueDispose")
	return nil
}

func spawnPCMCapture(sampleRate uint32, pcmCh chan<- []byte) (CaptureHandle, error) {
	if err := initAudioToolbox(); err != nil {
		return nil, err
	}
	desc := audioStreamBasicDescription{
		mSampleRate:       float64(sampleRate),
		mFormatID:         kAudioFormatLinearPCM,
		mFormatFlags:      kAudioFormatFlagIsSignedInt | kAudioFormatFlagIsPacked,
		mBytesPerPacket:   2,
		mFramesPerPacket:  1,
		mBytesPerFrame:    2,
		mChannelsPerFrame: 1,
		mBitsPerChannel:   16,
	}
	stop := &atomic.Bool{}
	cb := purego.NewCallback(func(_ unsafe.Pointer, inAQ uintptr, inBuffer uintptr, _, _, _ uintptr) {
		if stop.Load() {
			return
		}
		buf := (*audioQueueBuffer)(unsafe.Pointer(inBuffer))
		if buf != nil && buf.mAudioDataByteSize > 0 && buf.mAudioData != 0 {
			n := int(buf.mAudioDataByteSize)
			src := unsafe.Slice((*byte)(unsafe.Pointer(buf.mAudioData)), n)
			out := make([]byte, n)
			copy(out, src)
			trySendPCM(pcmCh, out, nil)
		}
		audioQueueEnqueueBuffer(inAQ, inBuffer, 0, 0)
	})
	var queue uintptr
	status := audioQueueNewInput(&desc, cb, nil, 0, 0, 0, &queue)
	if status != 0 {
		return nil, captureErr(fmt.Sprintf("AudioQueueNewInput failed: %d (grant mic permission in System Settings)", status))
	}
	bufSize := uint32(darwinBufferFrames * 2)
	for range darwinBufferCount {
		var buf uintptr
		if st := audioQueueAllocateBuffer(queue, bufSize, &buf); st != 0 {
			audioQueueDispose(queue, true)
			return nil, captureErr(fmt.Sprintf("AudioQueueAllocateBuffer failed: %d", st))
		}
		audioQueueEnqueueBuffer(queue, buf, 0, 0)
	}
	if st := audioQueueStart(queue, 0); st != 0 {
		audioQueueDispose(queue, true)
		return nil, captureErr(fmt.Sprintf("AudioQueueStart failed: %d (grant mic permission in System Settings)", st))
	}
	return &darwinHandle{stop: stop, queue: queue}, nil
}
