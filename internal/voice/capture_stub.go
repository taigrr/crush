//go:build !darwin && !linux && !windows

package voice

func spawnPCMCapture(_ uint32, _ string) (CaptureHandle, <-chan []byte, error) {
	return nil, nil, captureErr("voice audio capture is not supported on this platform")
}

func listInputDevices() ([]InputDevice, error) {
	return nil, captureErr("voice audio capture is not supported on this platform")
}
