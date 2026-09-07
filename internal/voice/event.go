package voice

type Event struct {
	Kind    EventKind
	Turn    int
	Text    string
	Message string
	Hint    string
}

type EventKind int

const (
	EventInterim EventKind = iota
	EventFinal
	EventError
	// EventStopped follows a released turn's final (if any).
	EventStopped
)
