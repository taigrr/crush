package voice

import "sync"

// Pushes after close are dropped rather than panicking: OS callback
// threads may fire once more after Stop.
type pcmStream struct {
	ch     chan []byte
	mu     sync.Mutex
	closed bool
}

func newPCMStream(buffer int) *pcmStream {
	return &pcmStream{ch: make(chan []byte, buffer)}
}

func (s *pcmStream) C() <-chan []byte { return s.ch }

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

func (s *pcmStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}
