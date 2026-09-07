package voice

import (
	"sync/atomic"
)

const captureReadChunk = 2048

// CaptureHandle stops an in-flight microphone capture.
type CaptureHandle interface {
	Stop()
}

// SpawnPCMCapture opens the default input device and forwards 16-bit
// little-endian mono PCM at sampleRate to pcmCh. The caller must Stop
// the returned handle to release the microphone.
func SpawnPCMCapture(sampleRate uint32, pcmCh chan<- []byte) (CaptureHandle, error) {
	return spawnPCMCapture(sampleRate, pcmCh)
}

func trySendPCM(pcmCh chan<- []byte, chunk []byte, dropped *atomic.Uint64) bool {
	select {
	case pcmCh <- chunk:
		return true
	default:
		if dropped != nil {
			dropped.Add(1)
		}
		return true
	}
}
