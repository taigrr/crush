//go:build darwin

package voice

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	kAudioFormatLinearPCM       = 0x6C70636D // 'lpcm'
	kAudioFormatFlagIsSignedInt = 1 << 2
	kAudioFormatFlagIsPacked    = 1 << 3
	darwinBufferCount           = 4
	darwinBufferFrames          = 1024

	// kAudioQueueProperty_CurrentDevice ('aqcd') selects the input device
	// for a queue by CoreAudio device UID.
	kAudioQueueProperty_CurrentDevice = 0x61716364
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
	mAudioData                 unsafe.Pointer
	mAudioDataByteSize         uint32
	mUserData                  uintptr
	mPacketDescriptionCapacity uint32
	mPacketDescriptions        uintptr
	mPacketDescriptionCount    uint32
}

type darwinHandle struct {
	once   sync.Once
	queue  uintptr
	sink   uintptr
	stream *pcmStream
}

func (h *darwinHandle) Stop() {
	if h == nil {
		return
	}
	h.once.Do(func() {
		if audioQueueStop != nil && h.queue != 0 {
			audioQueueStop(h.queue, true)
		}
		if audioQueueDispose != nil && h.queue != 0 {
			audioQueueDispose(h.queue, true)
			h.queue = 0
		}
		unregisterCaptureSink(h.sink)
		h.stream.close()
	})
}

// inputCallback is the single AudioQueue input trampoline shared by all
// captures; the sink id travels in inUserData.
var inputCallback = purego.NewCallback(func(inUserData uintptr, inAQ uintptr, buf *audioQueueBuffer, _, _, _ uintptr) {
	stream, ok := lookupCaptureSink(inUserData)
	if !ok {
		return
	}
	if buf != nil && buf.mAudioDataByteSize > 0 && buf.mAudioData != nil {
		n := int(buf.mAudioDataByteSize)
		src := unsafe.Slice((*byte)(buf.mAudioData), n)
		out := make([]byte, n)
		copy(out, src)
		stream.push(out)
	}
	audioQueueEnqueueBuffer(inAQ, buf, 0, 0)
})

var (
	audioOnce                sync.Once
	audioInitErr             error
	audioQueueNewInput       func(inFormat *audioStreamBasicDescription, inCallbackProc uintptr, inUserData uintptr, inCallbackRunLoop, inCallbackRunLoopMode uintptr, inFlags uint32, outAQ *uintptr) uintptr
	audioQueueAllocateBuffer func(inAQ uintptr, inBufferByteSize uint32, outBuffer **audioQueueBuffer) uintptr
	audioQueueEnqueueBuffer  func(inAQ uintptr, inBuffer *audioQueueBuffer, inNumPacketDescs uint32, inPacketDescs uintptr) uintptr
	audioQueueStart          func(inAQ uintptr, inStartTime uintptr) uintptr
	audioQueueStop           func(inAQ uintptr, inImmediate bool) uintptr
	audioQueueDispose        func(inAQ uintptr, inImmediate bool) uintptr
	audioQueueSetProperty    func(inAQ uintptr, inID uint32, inData unsafe.Pointer, inDataSize uint32) uintptr
)

func initAudioToolbox() error {
	audioOnce.Do(func() {
		toolbox, err := purego.Dlopen("/System/Library/Frameworks/AudioToolbox.framework/AudioToolbox", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			audioInitErr = captureErr("AudioToolbox: " + err.Error())
			return
		}
		purego.RegisterLibFunc(&audioQueueNewInput, toolbox, "AudioQueueNewInput")
		purego.RegisterLibFunc(&audioQueueAllocateBuffer, toolbox, "AudioQueueAllocateBuffer")
		purego.RegisterLibFunc(&audioQueueEnqueueBuffer, toolbox, "AudioQueueEnqueueBuffer")
		purego.RegisterLibFunc(&audioQueueStart, toolbox, "AudioQueueStart")
		purego.RegisterLibFunc(&audioQueueStop, toolbox, "AudioQueueStop")
		purego.RegisterLibFunc(&audioQueueDispose, toolbox, "AudioQueueDispose")
		purego.RegisterLibFunc(&audioQueueSetProperty, toolbox, "AudioQueueSetProperty")
	})
	return audioInitErr
}

func spawnPCMCapture(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
	if err := initAudioToolbox(); err != nil {
		return nil, nil, err
	}
	if deviceID != "" {
		if err := initCoreAudio(); err != nil {
			return nil, nil, err
		}
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
	stream := newPCMStream(captureBuffer)
	sink := registerCaptureSink(stream)
	fail := func(err *Error) (CaptureHandle, <-chan []byte, error) {
		unregisterCaptureSink(sink)
		stream.close()
		return nil, nil, err
	}
	var queue uintptr
	status := audioQueueNewInput(&desc, inputCallback, sink, 0, 0, 0, &queue)
	if status != 0 {
		return fail(captureErr(fmt.Sprintf("AudioQueueNewInput failed: %d (grant mic permission in System Settings)", status)))
	}
	if deviceID != "" {
		uid := cfStringCreate(deviceID)
		if uid == 0 {
			audioQueueDispose(queue, true)
			return fail(deviceUnavailableErr(deviceID))
		}
		st := audioQueueSetProperty(queue, kAudioQueueProperty_CurrentDevice, unsafe.Pointer(&uid), uint32(unsafe.Sizeof(uid)))
		cfRelease(uid)
		if st != 0 {
			audioQueueDispose(queue, true)
			return fail(deviceUnavailableErr(deviceID))
		}
	}
	bufSize := uint32(darwinBufferFrames * 2)
	for range darwinBufferCount {
		var buf *audioQueueBuffer
		if st := audioQueueAllocateBuffer(queue, bufSize, &buf); st != 0 {
			audioQueueDispose(queue, true)
			return fail(captureErr(fmt.Sprintf("AudioQueueAllocateBuffer failed: %d", st)))
		}
		audioQueueEnqueueBuffer(queue, buf, 0, 0)
	}
	if st := audioQueueStart(queue, 0); st != 0 {
		audioQueueDispose(queue, true)
		if deviceID != "" {
			return fail(deviceUnavailableErr(deviceID))
		}
		return fail(captureErr(fmt.Sprintf("AudioQueueStart failed: %d (grant mic permission in System Settings)", st)))
	}
	return &darwinHandle{queue: queue, sink: sink, stream: stream}, stream.C(), nil
}
