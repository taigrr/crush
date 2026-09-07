package voice

const captureBuffer = 64

// Stop closes the capture's PCM channel once already-recorded audio has
// been delivered.
type CaptureHandle interface {
	Stop()
}

func SpawnPCMCapture(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
	return spawnPCMCapture(sampleRate, deviceID)
}
