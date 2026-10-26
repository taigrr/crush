package app

import (
	"context"
	"time"

	"github.com/taigrr/crush/internal/csync"
	"github.com/taigrr/crush/internal/lsp"
	"github.com/taigrr/crush/internal/pubsub"
)

// LSPEventType represents the type of LSP event
type LSPEventType string

const (
	LSPEventStateChanged       LSPEventType = "state_changed"
	LSPEventDiagnosticsChanged LSPEventType = "diagnostics_changed"
)

// LSPEvent represents an event in the LSP system
type LSPEvent struct {
	Type            LSPEventType
	Name            string
	State           lsp.ServerState
	Error           error
	DiagnosticCount int
	Diagnostics     lsp.DiagnosticCounts
}

// LSPClientInfo holds information about an LSP client's state
type LSPClientInfo struct {
	Name            string
	State           lsp.ServerState
	Error           error
	Client          *lsp.Client
	DiagnosticCount int
	// Diagnostics breaks DiagnosticCount down by severity so clients can
	// render it from state instead of fetching every diagnostic.
	Diagnostics lsp.DiagnosticCounts
	ConnectedAt time.Time
}

// lspStatus holds one workspace's LSP client states and the broker that
// announces their changes. It is per App, not process-wide: a server hosts
// many workspaces, and each must report only its own LSP clients. A shared
// map leaked every workspace's clients into every other one, so a client
// asked its workspace for diagnostics of LSPs it never started.
type lspStatus struct {
	states *csync.Map[string, LSPClientInfo]
	broker *pubsub.Broker[LSPEvent]
}

func newLSPStatus() *lspStatus {
	return &lspStatus{
		states: csync.NewMap[string, LSPClientInfo](),
		broker: pubsub.NewBroker[LSPEvent](),
	}
}

// SubscribeLSPEvents returns a channel for this workspace's LSP events.
func (app *App) SubscribeLSPEvents(ctx context.Context) <-chan pubsub.Event[LSPEvent] {
	return app.lsp.broker.Subscribe(ctx)
}

// GetLSPStates returns the current state of this workspace's LSP clients.
func (app *App) GetLSPStates() map[string]LSPClientInfo {
	if app.lsp == nil {
		return map[string]LSPClientInfo{}
	}
	return app.lsp.states.Copy()
}

// updateLSPState updates the state of an LSP client and publishes an event
func (s *lspStatus) updateLSPState(name string, state lsp.ServerState, err error, client *lsp.Client, diagnosticCount int) {
	info := LSPClientInfo{
		Name:            name,
		State:           state,
		Error:           err,
		Client:          client,
		DiagnosticCount: diagnosticCount,
		Diagnostics:     client.GetDiagnosticCounts(),
	}
	if state == lsp.StateReady || state == lsp.StateWarn {
		info.ConnectedAt = time.Now()
	} else if existing, ok := s.states.Get(name); ok {
		info.ConnectedAt = existing.ConnectedAt
	}
	s.states.Set(name, info)

	// Publish state change event
	s.broker.Publish(pubsub.UpdatedEvent, LSPEvent{
		Type:            LSPEventStateChanged,
		Name:            name,
		State:           state,
		Error:           err,
		DiagnosticCount: diagnosticCount,
		Diagnostics:     info.Diagnostics,
	})
}

// updateLSPDiagnostics updates the diagnostic count for an LSP client and publishes an event
func (s *lspStatus) updateLSPDiagnostics(name string, diagnosticCount int) {
	if info, exists := s.states.Get(name); exists {
		info.DiagnosticCount = diagnosticCount
		info.Diagnostics = info.Client.GetDiagnosticCounts()
		s.states.Set(name, info)

		// Publish diagnostics change event
		s.broker.Publish(pubsub.UpdatedEvent, LSPEvent{
			Type:            LSPEventDiagnosticsChanged,
			Name:            name,
			State:           info.State,
			Error:           info.Error,
			DiagnosticCount: diagnosticCount,
			Diagnostics:     info.Diagnostics,
		})
	}
}
