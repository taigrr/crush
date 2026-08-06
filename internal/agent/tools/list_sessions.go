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

// NewListSessionsTool returns the list_sessions tool. It lists past
// conversations (id, title, message count, last activity) so the agent
// can find a session id to pass to search_history. The active session
// is marked so the agent can correlate "current" without a second call.
func NewListSessionsTool(sessions session.Service) fantasy.AgentTool {
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
			return fantasy.NewTextResponse(formatSessions(page, current, offset, total)), nil
		},
	)
}

// formatSessions renders one session per line, marking the active one
// and noting archived state. Full session ids are shown so they line up
// with search_history output and can be passed straight back in.
func formatSessions(sessions []session.Session, current string, offset, total int) string {
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
		archived := ""
		if s.ArchivedAt > 0 {
			archived = " [archived]"
		}
		address := ""
		if s.Color != "" && s.Animal != "" {
			address = swarm.FormatAddress(swarm.Identity{Color: s.Color, Animal: s.Animal}, s.ID) + "  "
		}
		fmt.Fprintf(&b, "%s %s  %s%q  (%d msgs, updated %s)%s\n",
			marker, s.ID, address, title, s.MessageCount,
			time.Unix(s.UpdatedAt, 0).Format(time.RFC3339), archived)
	}
	if last < total {
		fmt.Fprintf(&b, "\n%d more session(s). Pass offset=%d to see the next page.", total-last, last)
	}
	b.WriteString("\n(* = current session; pass the id, or 'current', to search_history)")
	return b.String()
}
