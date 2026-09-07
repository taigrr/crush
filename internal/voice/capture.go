package voice

// captureBuffer is how many PCM chunks a capture may queue before the
// consumer falls behind and chunks are dropped.
const captureBuffer = 64

// CaptureHandle stops an in-flight microphone capture. After Stop returns
// the capture's PCM channel is closed once any audio already recorded has
// been delivered.
type CaptureHandle interface {
	Stop()
}

// SpawnPCMCapture opens an input device and returns a channel of 16-bit
// little-endian mono PCM at sampleRate. deviceID is an [InputDevice.ID]
// from [ListInputDevices]; empty selects the system default. The capture
// owns the channel and closes it when stopped, so the caller must Stop
// the handle to release the microphone and end the stream.
func SpawnPCMCapture(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
	return spawnPCMCapture(sampleRate, deviceID)
}
