package headless

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/swarm"
)

// ReportClient is the subset of the Crush server client needed to enrich
// a [Result] with usage, file, and diagnostic information.
type ReportClient interface {
	GetSession(ctx context.Context, workspaceID, sessionID string) (*proto.Session, error)
	ListSessionHistoryFiles(ctx context.Context, workspaceID, sessionID string) ([]proto.File, error)
	GetLSPs(ctx context.Context, workspaceID string) (map[string]proto.LSPClientInfo, error)
	GetLSPDiagnostics(ctx context.Context, workspaceID, lspName string) (map[protocol.DocumentURI][]protocol.Diagnostic, error)
}

// Baseline captures session state before a run starts so the result can
// report only what this run consumed and changed.
type Baseline struct {
	Started     time.Time
	Session     proto.Session
	historyIDs  map[string]struct{}
	hasSnapshot bool
}

// TakeBaseline records the session's usage counters and file history
// before a run. Failures are logged and leave the baseline empty, in
// which case the result reports session totals instead of a delta.
func TakeBaseline(ctx context.Context, c ReportClient, workspaceID, sessionID string) Baseline {
	b := Baseline{Started: time.Now(), historyIDs: make(map[string]struct{})}
	if sess, err := c.GetSession(ctx, workspaceID, sessionID); err == nil && sess != nil {
		b.Session = *sess
	} else if err != nil {
		slog.Debug("Failed to read session for headless baseline", "error", err)
	}
	files, err := c.ListSessionHistoryFiles(ctx, workspaceID, sessionID)
	if err != nil {
		slog.Debug("Failed to read file history for headless baseline", "error", err)
		return b
	}
	for _, f := range files {
		b.historyIDs[f.ID] = struct{}{}
	}
	b.hasSnapshot = true
	return b
}

// Enrich fills the identity, usage, files-changed, and diagnostics
// fields of r for the given session. It is best effort: every lookup
// failure is logged and the corresponding field is left empty.
func (b Baseline) Enrich(ctx context.Context, c ReportClient, workspaceID, sessionID string, r *Result) {
	if !b.Started.IsZero() {
		r.DurationMS = time.Since(b.Started).Milliseconds()
	}
	r.SessionID = sessionID
	r.WorkspaceID = workspaceID

	if sess, err := c.GetSession(ctx, workspaceID, sessionID); err == nil && sess != nil {
		r.Address = SessionAddress(*sess)
		r.Usage = &Usage{
			PromptTokens:     nonNegative(sess.PromptTokens - b.Session.PromptTokens),
			CompletionTokens: nonNegative(sess.CompletionTokens - b.Session.CompletionTokens),
			CostUSD:          max(0, sess.Cost-b.Session.Cost),
		}
	} else if err != nil {
		slog.Debug("Failed to read session for headless result", "error", err)
	}

	files, err := c.ListSessionHistoryFiles(ctx, workspaceID, sessionID)
	if err != nil {
		slog.Debug("Failed to read file history for headless result", "error", err)
		return
	}
	r.FilesChanged = b.changedFiles(files)
	if len(r.FilesChanged) == 0 {
		return
	}
	r.Diagnostics = collectDiagnostics(ctx, c, workspaceID, r.FilesChanged)
}

// changedFiles returns the sorted, de-duplicated paths of history
// entries that did not exist when the baseline was taken.
func (b Baseline) changedFiles(files []proto.File) []string {
	seen := make(map[string]struct{})
	out := []string{}
	for _, f := range files {
		if b.hasSnapshot {
			if _, ok := b.historyIDs[f.ID]; ok {
				continue
			}
		}
		if f.Path == "" {
			continue
		}
		if _, ok := seen[f.Path]; ok {
			continue
		}
		seen[f.Path] = struct{}{}
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

func collectDiagnostics(ctx context.Context, c ReportClient, workspaceID string, files []string) *DiagnosticSummary {
	lsps, err := c.GetLSPs(ctx, workspaceID)
	if err != nil || len(lsps) == 0 {
		return nil
	}
	wanted := make(map[string]string, len(files))
	for _, f := range files {
		wanted[cleanPath(f)] = f
	}

	names := make([]string, 0, len(lsps))
	for name := range lsps {
		names = append(names, name)
	}
	slices.Sort(names)

	summary := &DiagnosticSummary{}
	for _, name := range names {
		diags, err := c.GetLSPDiagnostics(ctx, workspaceID, name)
		if err != nil {
			slog.Debug("Failed to read LSP diagnostics for headless result", "lsp", name, "error", err)
			continue
		}
		for uri, list := range diags {
			path, err := uri.Path()
			if err != nil {
				continue
			}
			file, ok := wanted[cleanPath(path)]
			if !ok {
				continue
			}
			for _, d := range list {
				severity := severityName(d.Severity)
				switch severity {
				case "error":
					summary.Errors++
				case "warning":
					summary.Warnings++
				default:
					continue
				}
				summary.Items = append(summary.Items, Diagnostic{
					File:     file,
					Line:     int(d.Range.Start.Line) + 1,
					Column:   int(d.Range.Start.Character) + 1,
					Severity: severity,
					Message:  d.Message,
					Source:   d.Source,
				})
			}
		}
	}
	slices.SortFunc(summary.Items, func(a, b Diagnostic) int {
		if a.Severity != b.Severity {
			// Errors before warnings.
			return strings.Compare(a.Severity, b.Severity)
		}
		if a.File != b.File {
			return strings.Compare(a.File, b.File)
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return a.Column - b.Column
	})
	if len(summary.Items) > MaxDiagnosticItems {
		summary.Items = summary.Items[:MaxDiagnosticItems]
	}
	return summary
}

func severityName(s protocol.DiagnosticSeverity) string {
	switch s {
	case protocol.SeverityError:
		return "error"
	case protocol.SeverityWarning:
		return "warning"
	default:
		return ""
	}
}

func cleanPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

func nonNegative(n int64) int64 {
	return max(0, n)
}

// SessionAddress returns the session's fully-qualified swarm address
// (color-animal-shorthash), or "" when the session has not been assigned
// an identity yet. The session ID is always a valid address too.
func SessionAddress(s proto.Session) string {
	if s.Color == "" || s.Animal == "" {
		return ""
	}
	return swarm.FormatAddress(swarm.Identity{Color: s.Color, Animal: s.Animal}, s.ID)
}

// AttachCommand returns the shell command that opens the session in the
// interactive TUI.
func AttachCommand(cwd, sessionID string) string {
	var b strings.Builder
	b.WriteString("crush")
	if cwd != "" {
		b.WriteString(" --cwd ")
		b.WriteString(shellQuote(cwd))
	}
	b.WriteString(" --session ")
	b.WriteString(shellQuote(sessionID))
	return b.String()
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./:@%+=,", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
