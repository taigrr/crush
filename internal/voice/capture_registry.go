package voice

import (
	"sync"
	"sync/atomic"
)

// Native callbacks are created once per process (purego/syscall never free
// them, and their pool is bounded), so each capture registers its stream
// here and passes the id through the callback's user-data slot.
var (
	captureSinks   sync.Map // uintptr -> *pcmStream
	captureSinkSeq atomic.Uintptr
)

func registerCaptureSink(stream *pcmStream) uintptr {
	id := captureSinkSeq.Add(1)
	captureSinks.Store(id, stream)
	return id
}

func lookupCaptureSink(id uintptr) (*pcmStream, bool) {
	v, ok := captureSinks.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*pcmStream), true
}

func unregisterCaptureSink(id uintptr) {
	captureSinks.Delete(id)
}
