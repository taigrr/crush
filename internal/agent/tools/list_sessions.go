package tools

import (
	"context"
	_ "embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/session"
	"github.com/taigrr/crush/internal/swarm"
)

const (
	ListSessionsToolName = "list_sessions"
	maxSessionsListed    = 100
)

//go:embed list_sessions.md
var listSessionsDescription string

// ListSessionsParams controls the listing. All fields are optional.
type ListSessionsParams struct {
	IncludeArchived bool `json:"include_archived,omitempty" description:"Include archived sessions (default false)"`
	Limit           int  `json:"limit,omitempty" description:"Max sessions to return per page, most recent first (default 50, max 100)"`
	Offset          int  `json:"offset,omitempty" description:"Number of sessions to skip for pagination (default 0)"`
}

// SessionBusyFunc reports whether a session has an in-flight agent turn.
// It may be nil, in which case no session is reported as running.
type SessionBusyFunc func(sessionID string) bool

// NewListSessionsTool returns the list_sessions tool. It lists past
// conversations (id, status, title, message count, last activity) so the
// agent can find a session id to pass to search_history or swarm. The
// active session is marked so the agent can correlate "current" without
// a second call.
func NewListSessionsTool(sessions session.Service, isBusy SessionBusyFunc) fantasy.AgentTool {
	return fantasy.NewParallelAgentTool(
		ListSessionsToolName,
		listSessionsDescription,
		func(ctx context.Context, params ListSessionsParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			limit := params.Limit
			if limit <= 0 {
				limit = 50
			}
			if limit > maxSessionsListed {
				limit = maxSessionsListed
			}
			offset := max(params.Offset, 0)

			all, err := sessions.List(ctx)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to list sessions: %s", err)), nil
			}
			if params.IncludeArchived {
				archived, err := sessions.ListArchived(ctx)
				if err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to list archived sessions: %s", err)), nil
				}
				all = append(all, archived...)
			}

			// Most recent activity first.
			sort.Slice(all, func(i, j int) bool {
				return all[i].UpdatedAt > all[j].UpdatedAt
			})

			current := GetSessionFromContext(ctx)
			total := len(all)
			if total == 0 {
				return fantasy.NewTextResponse("No sessions found."), nil
			}
			if offset >= total {
				return fantasy.NewTextResponse(fmt.Sprintf("No sessions at offset %d (only %d total).", offset, total)), nil
			}
			end := min(offset+limit, total)
			page := all[offset:end]
			return fantasy.NewTextResponse(formatSessions(page, current, offset, total, isBusy)), nil
		},
	)
}

// sessionStatus mirrors the sidebar inbox tiers: Running (in-flight turn),
// Unread (finished work nobody has looked at), Archived, else Read.
func sessionStatus(s session.Session, isBusy SessionBusyFunc) string {
	switch {
	case isBusy != nil && isBusy(s.ID):
		return "Running"
	case s.ArchivedAt > 0:
		return "Archived"
	case s.Unread():
		return "Unread"
	default:
		return "Read"
	}
}

// formatSessions renders one session per line, marking the active one
// and showing its status. Full session ids are shown so they line up
// with search_history output and can be passed straight back in.
func formatSessions(sessions []session.Session, current string, offset, total int, isBusy SessionBusyFunc) string {
	var b strings.Builder
	first := offset + 1
	last := offset + len(sessions)
	fmt.Fprintf(&b, "Sessions %d-%d of %d:\n\n", first, last, total)
	for _, s := range sessions {
		marker := " "
		if s.ID == current {
			marker = "*"
		}
		title := s.Title
		if title == "" {
			title = "(untitled)"
		}
		address := ""
		if s.Color != "" && s.Animal != "" {
			address = swarm.FormatAddress(swarm.Identity{Color: s.Color, Animal: s.Animal}, s.ID) + "  "
		}
		fmt.Fprintf(&b, "%s %s  %-8s %s%q  (%d msgs, updated %s)\n",
			marker, s.ID, sessionStatus(s, isBusy), address, title, s.MessageCount,
			time.Unix(s.UpdatedAt, 0).Format(time.RFC3339))
	}
	if last < total {
		fmt.Fprintf(&b, "\n%d more session(s). Pass offset=%d to see the next page.", total-last, last)
	}
	b.WriteString("\n(* = current session; status: Running = agent turn in flight, Unread = finished work not yet viewed, Read = idle, Archived; pass the id, or 'current', to search_history)")
	return b.String()
}
