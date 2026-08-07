package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/diff"
	"github.com/taigrr/crush/internal/filepathext"
	"github.com/taigrr/crush/internal/filetracker"
	"github.com/taigrr/crush/internal/fsext"
	"github.com/taigrr/crush/internal/history"
	"github.com/taigrr/crush/internal/lsp"
	"github.com/taigrr/crush/internal/permission"
)

const ReplaceSymbolToolName = "lsp_replace_symbol"

//go:embed replace_symbol.md
var replaceSymbolDescription string

// ReplaceSymbolParams locates a named symbol via LSP document symbols and
// mutates its full range. Operation selects replace/insert/delete; NewContent
// is required for every operation except delete.
type ReplaceSymbolParams struct {
	FilePath   string `json:"file_path" description:"The path to the file containing the symbol"`
	Symbol     string `json:"symbol" description:"The symbol name to target (e.g. function, method, type/class name)"`
	Operation  string `json:"operation,omitempty" description:"Operation to perform: 'replace' (default, replace the entire symbol), 'insert_before' (insert before the symbol), 'insert_after' (insert after the symbol), 'delete' (remove the symbol entirely)"`
	NewContent string `json:"new_content,omitempty" description:"The new content. Required for 'replace', 'insert_before', and 'insert_after'; ignored for 'delete'."`
}

// ReplaceSymbolPermissionsParams carries the diff data shown in the
// permission dialog.
type ReplaceSymbolPermissionsParams struct {
	FilePath   string `json:"file_path"`
	Symbol     string `json:"symbol"`
	Operation  string `json:"operation"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
}

// ReplaceSymbolResponseMetadata carries diff data for the renderer.
type ReplaceSymbolResponseMetadata struct {
	FilePath   string `json:"file_path"`
	Symbol     string `json:"symbol"`
	Operation  string `json:"operation"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
	Additions  int    `json:"additions"`
	Removals   int    `json:"removals"`
}

// symbolMatch is a flattened document-symbol hit used for locating the
// target and for reporting ambiguous candidates.
type symbolMatch struct {
	name string
	kind protocol.SymbolKind
	rng  protocol.Range
}

// NewReplaceSymbolTool returns the lsp_replace_symbol tool. It resolves the
// symbol's exact range through the language server's document symbols, then
// applies the edit through the same permission-gated, history/filetracker
// tracked path the edit tool uses.
func NewReplaceSymbolTool(
	lspManager *lsp.Manager,
	permissions permission.Service,
	files history.Service,
	filetracker filetracker.Service,
	workingDir WorkingDirFunc,
) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ReplaceSymbolToolName,
		replaceSymbolDescription,
		func(ctx context.Context, params ReplaceSymbolParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.FilePath == "" {
				return fantasy.NewTextErrorResponse("file_path is required"), nil
			}
			if params.Symbol == "" {
				return fantasy.NewTextErrorResponse("symbol is required"), nil
			}

			operation := params.Operation
			if operation == "" {
				operation = "replace"
			}
			switch operation {
			case "replace", "insert_before", "insert_after", "delete":
			default:
				return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid operation %q: must be replace, insert_before, insert_after, or delete", operation)), nil
			}
			if operation != "delete" && params.NewContent == "" {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("new_content is required for operation %q", operation)), nil
			}

			if lspManager.Clients().Len() == 0 {
				return fantasy.NewTextErrorResponse("no LSP clients available"), nil
			}

			wd := workingDir(ctx)
			absPath, err := filepath.Abs(filepathext.SmartJoin(wd, params.FilePath))
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid path: %s", err)), nil
			}

			// Start blocks until the servers for this path are up.
			lspManager.Start(ctx, absPath)

			var client *lsp.Client
			for c := range lspManager.Clients().Seq() {
				if c.HandlesFile(absPath) {
					client = c
					break
				}
			}
			if client == nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("no LSP client handles file (unsupported language?): %s", absPath)), nil
			}

			symbols, err := client.DocumentSymbols(ctx, absPath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to get document symbols: %s", err)), nil
			}

			// Whole-symbol edits require hierarchical DocumentSymbol
			// results whose Range covers the entire declaration. Flat
			// SymbolInformation results only carry a name-location range,
			// so replacing/deleting against them would truncate the
			// symbol to garbage. Reject them explicitly.
			if !symbolsAreHierarchical(symbols) {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("the language server for %s returns flat symbol information without full declaration ranges; whole-symbol edits are unsupported for this server. Use the edit tool instead.", absPath),
				), nil
			}

			matches := findSymbolMatches(symbols, params.Symbol)
			switch {
			case len(matches) == 0:
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("symbol '%s' not found in %s.\n%s", params.Symbol, absPath, availableSymbolsHint(symbols)),
				), nil
			case len(matches) > 1:
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("symbol '%s' is ambiguous: %d matches in %s.\n%s", params.Symbol, len(matches), absPath, candidatesHint(matches)),
				), nil
			}

			target := matches[0]

			// Whole-line editing only handles symbols that begin at column
			// 0 and own their lines. Symbols starting mid-line (indented
			// nested members, or sharing a line like `type A int; type B
			// int`) would have their leading prefix or a co-located sibling
			// destroyed. Reject them and defer to the edit tool.
			if !symbolStartsAtColumnZero(target.rng) {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf("symbol '%s' does not begin at column 0 (starts at column %d); it may be indented or share a line with another declaration. Use the edit tool for whole-symbol changes here.", params.Symbol, target.rng.Start.Character),
				), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.NewTextErrorResponse("session ID is required for replacing a symbol"), nil
			}

			fileInfo, err := os.Stat(absPath)
			if err != nil {
				if os.IsNotExist(err) {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("file not found: %s", absPath)), nil
				}
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to access file: %s", err)), nil
			}
			if fileInfo.IsDir() {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("path is a directory, not a file: %s", absPath)), nil
			}

			// Read-before-edit guard: the model must have viewed the file
			// this session so it is editing content it has actually seen.
			lastRead := filetracker.LastReadTime(ctx, sessionID, absPath)
			if lastRead.IsZero() {
				return fantasy.NewTextErrorResponse("you must read the file before editing it. Use the View tool first"), nil
			}

			// Stale-file guard: refuse to clobber changes made out of band
			// since the file was last read.
			modTime := fileInfo.ModTime().Truncate(time.Second)
			if modTime.After(lastRead) {
				return fantasy.NewTextErrorResponse(
					fmt.Sprintf(
						"file %s has been modified since it was last read (mod time: %s, last read: %s)",
						absPath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339),
					),
				), nil
			}

			raw, err := os.ReadFile(absPath)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to read file: %s", err)), nil
			}
			oldContent, isCrlf := fsext.ToUnixLineEndings(string(raw))

			newContent, err := applySymbolEdit(oldContent, target.rng, operation, params.NewContent)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}
			if newContent == oldContent {
				return fantasy.NewTextErrorResponse("new content is the same as old content. No changes made."), nil
			}

			_, additions, removals := diff.GenerateDiff(
				oldContent,
				newContent,
				fsext.PathOrPrefix(absPath, wd),
			)

			p, err := permissions.Request(ctx, permission.CreatePermissionRequest{
				SessionID:   sessionID,
				Path:        fsext.PathOrPrefix(absPath, wd),
				ToolCallID:  call.ID,
				ToolName:    ReplaceSymbolToolName,
				Action:      "write",
				Description: fmt.Sprintf("%s symbol '%s' in %s", operation, params.Symbol, absPath),
				Params: ReplaceSymbolPermissionsParams{
					FilePath:   absPath,
					Symbol:     params.Symbol,
					Operation:  operation,
					OldContent: oldContent,
					NewContent: newContent,
				},
			})
			if err != nil {
				return fantasy.ToolResponse{}, err
			}
			if !p {
				resp := NewPermissionDeniedResponse()
				resp = fantasy.WithResponseMetadata(resp, ReplaceSymbolResponseMetadata{
					FilePath:   absPath,
					Symbol:     params.Symbol,
					Operation:  operation,
					OldContent: oldContent,
					NewContent: newContent,
					Additions:  additions,
					Removals:   removals,
				})
				return resp, nil
			}

			writeContent := newContent
			if isCrlf {
				writeContent, _ = fsext.ToWindowsLineEndings(newContent)
			}
			if err := os.WriteFile(absPath, []byte(writeContent), 0o644); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to write file: %s", err)), nil
			}

			// History bookkeeping mirrors the edit tool: create a base
			// record if absent, store an intermediate version if the user
			// changed the file out of band, then store the new version.
			file, err := files.GetByPathAndSession(ctx, absPath, sessionID)
			if err != nil {
				if _, err := files.Create(ctx, sessionID, absPath, oldContent); err != nil {
					return fantasy.NewTextErrorResponse(fmt.Sprintf("file was written but recording its history failed: %s", err)), nil
				}
			} else if file.Content != oldContent {
				if _, err := files.CreateVersion(ctx, sessionID, absPath, oldContent); err != nil {
					slog.Debug("Error creating file history version", "error", err)
				}
			}
			if _, err := files.CreateVersion(ctx, sessionID, absPath, newContent); err != nil {
				slog.Error("Error creating file history version", "error", err)
			}

			filetracker.RecordRead(ctx, sessionID, absPath)
			notifyEditor(ctx, absPath, oldContent, newContent)
			notifyLSPs(ctx, lspManager, absPath)

			summary := formatReplaceSymbolResult(operation, params.Symbol, absPath, target.rng)
			text := fmt.Sprintf("<result>\n%s\n</result>\n", summary)
			text += getDiagnostics(absPath, lspManager)

			resp := fantasy.NewTextResponse(text)
			resp = fantasy.WithResponseMetadata(resp, ReplaceSymbolResponseMetadata{
				FilePath:   absPath,
				Symbol:     params.Symbol,
				Operation:  operation,
				OldContent: oldContent,
				NewContent: newContent,
				Additions:  additions,
				Removals:   removals,
			})
			return resp, nil
		},
	)
}

// symbolsAreHierarchical reports whether the document symbol results are
// hierarchical DocumentSymbol values (whose Range spans the whole
// declaration) rather than flat SymbolInformation (name-location only).
func symbolsAreHierarchical(symbols []protocol.DocumentSymbolResult) bool {
	for _, sym := range symbols {
		if _, ok := sym.(*protocol.DocumentSymbol); !ok {
			return false
		}
	}
	return true
}

// findSymbolMatches walks the hierarchical document symbol tree and returns
// every symbol whose name matches, flattened.
func findSymbolMatches(symbols []protocol.DocumentSymbolResult, name string) []symbolMatch {
	var matches []symbolMatch
	var walk func([]protocol.DocumentSymbolResult)
	walk = func(syms []protocol.DocumentSymbolResult) {
		for _, sym := range syms {
			if sym.GetName() == name {
				matches = append(matches, symbolMatch{
					name: sym.GetName(),
					kind: symbolKindOf(sym),
					rng:  sym.GetRange(),
				})
			}
			if ds, ok := sym.(*protocol.DocumentSymbol); ok && len(ds.Children) > 0 {
				children := make([]protocol.DocumentSymbolResult, len(ds.Children))
				for i := range ds.Children {
					children[i] = &ds.Children[i]
				}
				walk(children)
			}
		}
	}
	walk(symbols)
	return matches
}

// symbolKindOf extracts the SymbolKind from either concrete result type.
func symbolKindOf(sym protocol.DocumentSymbolResult) protocol.SymbolKind {
	switch v := sym.(type) {
	case *protocol.DocumentSymbol:
		return v.Kind
	case *protocol.SymbolInformation:
		return v.Kind
	default:
		return 0
	}
}

// symbolStartsAtColumnZero reports whether the symbol begins at column 0.
// Whole-line editing is only safe for such symbols; ones starting mid-line
// are indented nested members or share a line with another declaration.
func symbolStartsAtColumnZero(rng protocol.Range) bool {
	return rng.Start.Character == 0
}

// normalizedEndLine returns the 0-based index of the last physical line the
// range actually covers. LSP ranges are end-exclusive: gopls reports the
// closing line as End.Line, but spec-compliant servers (rust-analyzer,
// tsserver) report {nextLine, char:0}. Both the edit and the human-readable
// messages must agree, so the normalization lives here and is reused.
func normalizedEndLine(rng protocol.Range) int {
	endLine := int(rng.End.Line)
	if rng.End.Character == 0 && rng.End.Line > rng.Start.Line {
		endLine--
	}
	return endLine
}

// applySymbolEdit rewrites the symbol's line range within content according
// to operation. content uses unix line endings.
func applySymbolEdit(content string, rng protocol.Range, operation, newText string) (string, error) {
	lines := strings.Split(content, "\n")
	startLine := int(rng.Start.Line)
	endLine := normalizedEndLine(rng)

	if startLine < 0 || startLine >= len(lines) || endLine < startLine || endLine >= len(lines) {
		return "", fmt.Errorf("symbol range (%d-%d) exceeds file length (%d lines)", startLine+1, endLine+1, len(lines))
	}

	// A trailing newline in the inserted text would split into a spurious
	// empty final element; drop one trailing "\n" so it joins cleanly.
	insert := strings.Split(strings.TrimSuffix(newText, "\n"), "\n")

	var newLines []string
	switch operation {
	case "replace":
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, insert...)
		newLines = append(newLines, lines[endLine+1:]...)
	case "insert_before":
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, insert...)
		newLines = append(newLines, lines[startLine:]...)
	case "insert_after":
		newLines = append(newLines, lines[:endLine+1]...)
		newLines = append(newLines, insert...)
		newLines = append(newLines, lines[endLine+1:]...)
	case "delete":
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, lines[endLine+1:]...)
	default:
		return "", fmt.Errorf("invalid operation %q", operation)
	}

	return strings.Join(newLines, "\n"), nil
}

// availableSymbolsHint renders the top-level symbol outline so the model can
// pick a valid name after a not-found error.
func availableSymbolsHint(symbols []protocol.DocumentSymbolResult) string {
	if len(symbols) == 0 {
		return "No symbols found in file."
	}
	var b strings.Builder
	b.WriteString("Available symbols:\n")
	for _, s := range symbols {
		fmt.Fprintf(&b, "  %s %s (line %d)\n", symbolKindName(symbolKindOf(s)), s.GetName(), s.GetRange().Start.Line+1)
	}
	return b.String()
}

// candidatesHint lists every matching symbol with its kind and line so the
// caller can disambiguate.
func candidatesHint(matches []symbolMatch) string {
	var b strings.Builder
	b.WriteString("Candidates:\n")
	for _, m := range matches {
		fmt.Fprintf(&b, "  %s %s (lines %d-%d)\n", symbolKindName(m.kind), m.name, m.rng.Start.Line+1, normalizedEndLine(m.rng)+1)
	}
	b.WriteString("Symbol names must be unique within a file for this tool. Use the edit tool for finer control.")
	return b.String()
}

func formatReplaceSymbolResult(operation, symbol, path string, rng protocol.Range) string {
	start := rng.Start.Line + 1
	end := uint32(normalizedEndLine(rng)) + 1
	switch operation {
	case "replace":
		return fmt.Sprintf("Replaced symbol '%s' in %s (lines %d-%d)", symbol, path, start, end)
	case "insert_before":
		return fmt.Sprintf("Inserted before symbol '%s' in %s (before line %d)", symbol, path, start)
	case "insert_after":
		return fmt.Sprintf("Inserted after symbol '%s' in %s (after line %d)", symbol, path, end)
	case "delete":
		return fmt.Sprintf("Deleted symbol '%s' from %s (lines %d-%d)", symbol, path, start, end)
	default:
		return fmt.Sprintf("Modified symbol '%s' in %s", symbol, path)
	}
}
