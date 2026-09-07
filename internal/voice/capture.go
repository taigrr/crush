package voice

const captureBuffer = 64

type CaptureHandle interface {
	Stop()
}

func SpawnPCMCapture(sampleRate uint32, deviceID string) (CaptureHandle, <-chan []byte, error) {
	return spawnPCMCapture(sampleRate, deviceID)
}
