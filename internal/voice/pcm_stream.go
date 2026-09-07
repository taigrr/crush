package voice

import "sync"

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
