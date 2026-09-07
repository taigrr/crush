package voice

// Event is emitted by the capture/STT pipeline to the TUI event loop.
type Event struct {
	Kind EventKind
	// Turn is the id passed to [Press] for the turn that produced this
	// event.
	Turn int
	// Text is the transcript for Interim and Final events.
	Text string
	// Message is a short error description for Error events.
	Message string
	// Hint is optional longer fix steps for Error events.
	Hint string
}

// EventKind classifies a pipeline [Event].
type EventKind int

const (
	// EventInterim is a partial transcript while the user is speaking.
	EventInterim EventKind = iota
	// EventFinal is a completed utterance (speech_final or transcript.done).
	EventFinal
	// EventError is a non-fatal or fatal capture/STT failure.
	EventError
	// EventStopped marks the end of a released turn: the final transcript
	// (if any) has already been delivered and the microphone is closed.
	EventStopped
)
