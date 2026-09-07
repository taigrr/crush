//go:build !darwin && !linux && !windows

package voice

func spawnPCMCapture(sampleRate uint32, pcmCh chan<- []byte) (CaptureHandle, error) {
	return nil, captureErr("voice audio capture is not supported on this platform")
}
