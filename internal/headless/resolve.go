package headless

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

// SessionLister is the subset of the client used by [ResolveSession].
type SessionLister interface {
	GetSession(ctx context.Context, workspaceID, sessionID string) (*proto.Session, error)
	ListSessions(ctx context.Context, workspaceID string) ([]proto.Session, error)
}

// ResolveSession resolves a session in one workspace from a full ID, a
// hash prefix as shown by `crush session list`, or a swarm address
// (color-animal[-shorthash]).
func ResolveSession(ctx context.Context, c SessionLister, workspaceID, ref string) (*proto.Session, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("no session given")
	}
	if sess, err := c.GetSession(ctx, workspaceID, ref); err == nil && sess != nil {
		return sess, nil
	}
	sessions, err := c.ListSessions(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	var matches []proto.Session
	if addr, ok := swarm.ParseAddress(ref); ok && addr.Color != "" {
		for _, s := range sessions {
			if addr.MatchesColorAnimal(s.Color, s.Animal, s.ID) {
				matches = append(matches, s)
			}
		}
	}
	if len(matches) == 0 {
		lower := strings.ToLower(ref)
		for _, s := range sessions {
			if strings.HasPrefix(session.HashID(s.ID), lower) {
				matches = append(matches, s)
			}
		}
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("session %q not found", ref)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("session %q is ambiguous (%d matches)", ref, len(matches))
	}
}

// SwarmResolver resolves swarm addresses across every workspace the
// server knows about.
type SwarmResolver interface {
	ResolveSwarmAddress(ctx context.Context, address string) (proto.SwarmTarget, error)
}

// LocatorClient can look sessions up locally and across workspaces.
type LocatorClient interface {
	SessionLister
	SwarmResolver
}

// LocateSession resolves ref in workspaceID and, failing that, across
// all workspaces. When the session lives in another workspace it
// returns that workspace's target instead of a session; the caller
// connects to target.Path and fetches target.SessionID there.
func LocateSession(ctx context.Context, c LocatorClient, workspaceID, ref string) (*proto.Session, *proto.SwarmTarget, error) {
	sess, err := ResolveSession(ctx, c, workspaceID, ref)
	if err == nil {
		return sess, nil, nil
	}
	target, swarmErr := c.ResolveSwarmAddress(ctx, ref)
	if swarmErr != nil || target.SessionID == "" {
		return nil, nil, fmt.Errorf("%w (looked in this project and across all known workspaces)", err)
	}
	if target.WorkspaceID == workspaceID {
		sess, err := c.GetSession(ctx, workspaceID, target.SessionID)
		if err != nil {
			return nil, nil, err
		}
		return sess, nil, nil
	}
	if target.Path == "" {
		return nil, nil, fmt.Errorf("session %q belongs to a workspace with no known path", ref)
	}
	return nil, &target, nil
}
