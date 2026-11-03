package cluster

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/jwt/v2"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

// ClientsAccount is the NATS account paired devices live in. Mesh
// traffic stays in the global account, so no device permission can reach
// a mesh subject.
const ClientsAccount = "CLIENTS"

// meshUser is the in-process mesh connection's user name.
const meshUser = "crush-mesh"

const (
	wsHandshakeTimeout = 5 * time.Second
	// responseTTL bounds how long a device may answer a request it
	// received (allow_responses); it may answer each one once.
	responseTTL = time.Minute
)

// ClientUser is a device NATS user: its nkey and allowed subjects.
// Everything not listed is denied, for both publish and subscribe.
type ClientUser struct {
	Nkey      string
	Publish   []string
	Subscribe []string
	// ConnectionTypes overrides the allowed connection types (tests);
	// devices connect over the websocket only.
	ConnectionTypes []string
}

func connTypes(types ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(types))
	for _, t := range types {
		out[t] = struct{}{}
	}
	return out
}

// WebsocketAddr is the loopback address of the embedded server's device
// websocket listener (plain; front it with TLS).
func (m *Mesh) WebsocketAddr() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.wsAddr
}

// SetClientUsers replaces the device users in the CLIENTS account.
// Removed users are disconnected by the server on reload.
func (m *Mesh) SetClientUsers(users []ClientUser) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.srv == nil {
		return errors.New("mesh is not running")
	}
	next := m.opts.Clone()
	acc := clientsAccount(next)
	if acc == nil {
		return errors.New("mesh has no clients account")
	}
	next.Nkeys = make([]*natsserver.NkeyUser, 0, len(users))
	for _, u := range users {
		types := u.ConnectionTypes
		if len(types) == 0 {
			types = []string{jwt.ConnectionTypeWebsocket}
		}
		next.Nkeys = append(next.Nkeys, &natsserver.NkeyUser{
			Nkey:                   u.Nkey,
			Account:                acc,
			AllowedConnectionTypes: connTypes(types...),
			Permissions: &natsserver.Permissions{
				Publish:   scoped(u.Publish),
				Subscribe: scoped(u.Subscribe),
				Response:  &natsserver.ResponsePermission{MaxMsgs: 1, Expires: responseTTL},
			},
		})
	}
	if err := m.srv.ReloadOptions(next); err != nil {
		return fmt.Errorf("reload mesh users: %w", err)
	}
	m.opts = next
	slog.Info("Mesh device users updated", "devices", len(users))
	return nil
}

// scoped is a deny-by-default subject permission: only allow is
// permitted, and an empty allow list (which NATS reads as "allow all")
// becomes an explicit deny of everything.
func scoped(allow []string) *natsserver.SubjectPermission {
	if len(allow) == 0 {
		return &natsserver.SubjectPermission{Deny: []string{">"}}
	}
	return &natsserver.SubjectPermission{Allow: allow}
}

func clientsAccount(opts *natsserver.Options) *natsserver.Account {
	for _, a := range opts.Accounts {
		if a.Name == ClientsAccount {
			return a
		}
	}
	return nil
}

// Server returns the embedded NATS server (tests and in-process device
// bridges).
func (m *Mesh) Server() *natsserver.Server {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srv
}
