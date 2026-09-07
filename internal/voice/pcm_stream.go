package voice

import "sync"

// pcmStream is the channel a capture delivers PCM on. The capture is its
// sole writer and closes it when the capture ends, so a consumer can
// simply range over C() and treat channel close as end of audio. Pushes
// after close are dropped rather than panicking, which is what the OS
// callback threads and pipe readers that feed captures need.
type pcmStream struct {
	ch     chan []byte
	mu     sync.Mutex
	closed bool
}

func newPCMStream(buffer int) *pcmStream {
	return &pcmStream{ch: make(chan []byte, buffer)}
}

// C is the read side.
func (s *pcmStream) C() <-chan []byte { return s.ch }

// push enqueues a chunk without blocking; a full buffer drops the chunk.
func (s *pcmStream) push(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- chunk:
	default:
	}
}

// close ends the stream. Idempotent.
func (s *pcmStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}
