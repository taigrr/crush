package headless

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

type fakeReportClient struct {
	sessions    []proto.Session
	sessionCall int
	sessionErr  error
	files       [][]proto.File
	filesCall   int
	filesErr    error
	lsps        map[string]proto.LSPClientInfo
	lspsErr     error
	diags       map[string]map[protocol.DocumentURI][]protocol.Diagnostic
	diagErr     map[string]error
}

func (f *fakeReportClient) GetSession(context.Context, string, string) (*proto.Session, error) {
	if f.sessionErr != nil {
		return nil, f.sessionErr
	}
	s := f.sessions[min(f.sessionCall, len(f.sessions)-1)]
	f.sessionCall++
	return &s, nil
}

func (f *fakeReportClient) ListSessionHistoryFiles(context.Context, string, string) ([]proto.File, error) {
	if f.filesErr != nil {
		return nil, f.filesErr
	}
	files := f.files[min(f.filesCall, len(f.files)-1)]
	f.filesCall++
	return files, nil
}

func (f *fakeReportClient) GetLSPs(context.Context, string) (map[string]proto.LSPClientInfo, error) {
	return f.lsps, f.lspsErr
}

func (f *fakeReportClient) GetLSPDiagnostics(_ context.Context, _ string, name string) (map[protocol.DocumentURI][]protocol.Diagnostic, error) {
	if err := f.diagErr[name]; err != nil {
		return nil, err
	}
	return f.diags[name], nil
}

func diag(sev protocol.DiagnosticSeverity, line, col uint32, msg string) protocol.Diagnostic {
	return protocol.Diagnostic{
		Severity: sev,
		Message:  msg,
		Source:   "gopls",
		Range:    protocol.Range{Start: protocol.Position{Line: line, Character: col}},
	}
}

func TestBaselineEnrich_ReportsDeltaFilesAndDiagnostics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mainGo := filepath.Join(dir, "main.go")
	utilGo := filepath.Join(dir, "util.go")
	oldGo := filepath.Join(dir, "old.go")

	c := &fakeReportClient{
		sessions: []proto.Session{
			{ID: "sess-0000-1111", PromptTokens: 100, CompletionTokens: 10, Cost: 0.5},
			{ID: "sess-0000-1111", PromptTokens: 350, CompletionTokens: 60, Cost: 0.75, Color: "red", Animal: "fox"},
		},
		files: [][]proto.File{
			{{ID: "f0", Path: oldGo}},
			{
				{ID: "f0", Path: oldGo},
				{ID: "f1", Path: utilGo},
				{ID: "f2", Path: mainGo},
				{ID: "f3", Path: mainGo},
				{ID: "f4", Path: ""},
			},
		},
		lsps: map[string]proto.LSPClientInfo{"gopls": {Name: "gopls"}, "broken": {Name: "broken"}},
		diags: map[string]map[protocol.DocumentURI][]protocol.Diagnostic{
			"gopls": {
				protocol.URIFromPath(mainGo): {
					diag(protocol.SeverityWarning, 9, 0, "unused var"),
					diag(protocol.SeverityError, 4, 2, "undefined: foo"),
					diag(protocol.SeverityHint, 1, 1, "hint is dropped"),
				},
				protocol.URIFromPath(oldGo): {
					diag(protocol.SeverityError, 0, 0, "not touched by this run"),
				},
			},
		},
		diagErr: map[string]error{"broken": errors.New("lsp down")},
	}

	base := TakeBaseline(t.Context(), c, "ws", "sess-0000-1111")
	base.Started = time.Now().Add(-1500 * time.Millisecond)

	r := NewResult(StatusCompleted)
	base.Enrich(t.Context(), c, "ws", "sess-0000-1111", &r)

	require.Equal(t, "sess-0000-1111", r.SessionID)
	require.Equal(t, "ws", r.WorkspaceID)
	require.Equal(t, "red-fox-1111", r.Address)
	require.GreaterOrEqual(t, r.DurationMS, int64(1500))
	require.Equal(t, &Usage{PromptTokens: 250, CompletionTokens: 50, CostUSD: 0.25}, r.Usage)
	require.Equal(t, []string{mainGo, utilGo}, r.FilesChanged)

	require.NotNil(t, r.Diagnostics)
	require.Equal(t, 1, r.Diagnostics.Errors)
	require.Equal(t, 1, r.Diagnostics.Warnings)
	require.Equal(t, []Diagnostic{
		{File: mainGo, Line: 5, Column: 3, Severity: "error", Message: "undefined: foo", Source: "gopls"},
		{File: mainGo, Line: 10, Column: 1, Severity: "warning", Message: "unused var", Source: "gopls"},
	}, r.Diagnostics.Items)
}

func TestBaselineEnrich_NoChangesSkipsDiagnostics(t *testing.T) {
	t.Parallel()

	c := &fakeReportClient{
		sessions: []proto.Session{{ID: "s"}},
		files:    [][]proto.File{{{ID: "f0", Path: "/a"}}},
		lspsErr:  errors.New("must not be called"),
	}
	base := TakeBaseline(t.Context(), c, "ws", "s")
	r := NewResult(StatusCompleted)
	base.Enrich(t.Context(), c, "ws", "s", &r)
	require.Empty(t, r.FilesChanged)
	require.NotNil(t, r.FilesChanged)
	require.Nil(t, r.Diagnostics)
	require.Empty(t, r.Address, "sessions without an identity have no swarm address yet")
}

func TestBaselineEnrich_LookupFailuresAreBestEffort(t *testing.T) {
	t.Parallel()

	c := &fakeReportClient{
		sessionErr: errors.New("gone"),
		filesErr:   errors.New("gone"),
	}
	base := TakeBaseline(t.Context(), c, "ws", "s")
	r := NewResult(StatusError)
	base.Enrich(t.Context(), c, "ws", "s", &r)
	require.Equal(t, "s", r.SessionID)
	require.Nil(t, r.Usage)
	require.Empty(t, r.FilesChanged)
}

func TestBaselineEnrich_MissingBaselineReportsAllHistory(t *testing.T) {
	t.Parallel()

	c := &fakeReportClient{
		sessions: []proto.Session{{ID: "s", PromptTokens: 7}},
		files:    [][]proto.File{{{ID: "f0", Path: "/b"}, {ID: "f1", Path: "/a"}}},
	}
	// A zero Baseline (no snapshot) reports every touched file and the
	// session totals, which is what `crush session wait` needs when it
	// was not around for the start of the run.
	r := NewResult(StatusCompleted)
	Baseline{}.Enrich(t.Context(), c, "ws", "s", &r)
	require.Equal(t, []string{"/a", "/b"}, r.FilesChanged)
	require.Equal(t, int64(7), r.Usage.PromptTokens)
	require.Zero(t, r.DurationMS)
}

func TestCollectDiagnosticsCapsItems(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "x.go")
	var list []protocol.Diagnostic
	for i := range MaxDiagnosticItems + 10 {
		list = append(list, diag(protocol.SeverityError, uint32(i), 0, "e"))
	}
	c := &fakeReportClient{
		lsps:  map[string]proto.LSPClientInfo{"gopls": {}},
		diags: map[string]map[protocol.DocumentURI][]protocol.Diagnostic{"gopls": {protocol.URIFromPath(file): list}},
	}
	s := collectDiagnostics(t.Context(), c, "ws", []string{file})
	require.Equal(t, MaxDiagnosticItems+10, s.Errors, "counts are not capped")
	require.Len(t, s.Items, MaxDiagnosticItems)
}

func TestAttachCommand(t *testing.T) {
	t.Parallel()

	require.Equal(t, "crush --cwd /home/me/proj --session abc-123", AttachCommand("/home/me/proj", "abc-123"))
	require.Equal(t, "crush --session abc", AttachCommand("", "abc"))
	require.Equal(t, `crush --cwd '/tmp/my proj' --session abc`, AttachCommand("/tmp/my proj", "abc"))
	require.Equal(t, `crush --cwd '/tmp/it'\''s' --session abc`, AttachCommand("/tmp/it's", "abc"))
	require.Equal(t, `crush --session ''`, AttachCommand("", ""))
}
