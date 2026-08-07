package tools

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// rng builds a gopls-style inclusive-looking range whose End points at a
// non-zero character on the last content line (so end-exclusive
// normalization is a no-op).
func rng(startLine, endLine uint32) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: startLine},
		End:   protocol.Position{Line: endLine, Character: 1},
	}
}

func docSym(name string, kind protocol.SymbolKind, startLine, endLine uint32, children ...protocol.DocumentSymbol) *protocol.DocumentSymbol {
	return &protocol.DocumentSymbol{
		Name:     name,
		Kind:     kind,
		Range:    rng(startLine, endLine),
		Children: children,
	}
}

const sampleFile = "package main\n" + // line 0
	"\n" + // line 1
	"func Foo() {\n" + // line 2
	"\tprintln(\"foo\")\n" + // line 3
	"}\n" + // line 4
	"\n" + // line 5
	"func Bar() {\n" + // line 6
	"\tprintln(\"bar\")\n" + // line 7
	"}\n" // line 8 (trailing "")

func TestApplySymbolEdit_Replace(t *testing.T) {
	t.Parallel()
	// Foo occupies lines 2-4.
	got, err := applySymbolEdit(sampleFile, rng(2, 4), "replace", "func Foo() {\n\tprintln(\"FOO\")\n}")
	require.NoError(t, err)
	require.Contains(t, got, "println(\"FOO\")")
	require.NotContains(t, got, "println(\"foo\")")
	require.Contains(t, got, "func Bar()") // untouched
}

func TestApplySymbolEdit_InsertBefore(t *testing.T) {
	t.Parallel()
	got, err := applySymbolEdit(sampleFile, rng(2, 4), "insert_before", "// new comment")
	require.NoError(t, err)
	lines := strings.Split(got, "\n")
	require.Equal(t, "// new comment", lines[2])
	require.Equal(t, "func Foo() {", lines[3])
}

func TestApplySymbolEdit_InsertAfter(t *testing.T) {
	t.Parallel()
	got, err := applySymbolEdit(sampleFile, rng(2, 4), "insert_after", "// trailing")
	require.NoError(t, err)
	lines := strings.Split(got, "\n")
	// Foo ends on line 4 ("}"), insertion follows it.
	require.Equal(t, "}", lines[4])
	require.Equal(t, "// trailing", lines[5])
}

func TestApplySymbolEdit_Delete(t *testing.T) {
	t.Parallel()
	got, err := applySymbolEdit(sampleFile, rng(2, 4), "delete", "")
	require.NoError(t, err)
	require.NotContains(t, got, "func Foo()")
	require.Contains(t, got, "func Bar()")
}

func TestApplySymbolEdit_OutOfRange(t *testing.T) {
	t.Parallel()
	_, err := applySymbolEdit(sampleFile, rng(100, 200), "replace", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds file length")
}

func TestApplySymbolEdit_EndExclusiveRange(t *testing.T) {
	t.Parallel()
	// A spec-compliant server reports Foo's end as {line 5, char 0}
	// (exclusive). Normalization must treat line 4 as the last content
	// line so Bar (line 6) survives.
	excl := protocol.Range{
		Start: protocol.Position{Line: 2},
		End:   protocol.Position{Line: 5, Character: 0},
	}
	got, err := applySymbolEdit(sampleFile, excl, "delete", "")
	require.NoError(t, err)
	require.NotContains(t, got, "func Foo()")
	require.Contains(t, got, "func Bar()")
	// The blank line 5 between Foo and Bar must be preserved, not eaten.
	lines := strings.Split(got, "\n")
	require.Equal(t, "", lines[2])
	require.Equal(t, "func Bar() {", lines[3])
}

func TestApplySymbolEdit_TrailingNewline(t *testing.T) {
	t.Parallel()
	// new_content ending in "\n" must not introduce a spurious blank line.
	got, err := applySymbolEdit(sampleFile, rng(2, 4), "replace", "func Foo() {}\n")
	require.NoError(t, err)
	lines := strings.Split(got, "\n")
	require.Equal(t, "func Foo() {}", lines[2])
	require.Equal(t, "", lines[3]) // original blank line 5, not an extra one
	require.Equal(t, "func Bar() {", lines[4])
}

func TestSymbolsAreHierarchical(t *testing.T) {
	t.Parallel()
	hierarchical := []protocol.DocumentSymbolResult{
		docSym("Foo", protocol.Function, 2, 4),
	}
	require.True(t, symbolsAreHierarchical(hierarchical))

	flat := []protocol.DocumentSymbolResult{
		&protocol.SymbolInformation{Name: "Foo", Kind: protocol.Function},
	}
	require.False(t, symbolsAreHierarchical(flat))

	require.True(t, symbolsAreHierarchical(nil))
}

func TestFindSymbolMatches_TopLevel(t *testing.T) {
	t.Parallel()
	symbols := []protocol.DocumentSymbolResult{
		docSym("Foo", protocol.Function, 2, 4),
		docSym("Bar", protocol.Function, 6, 8),
	}
	matches := findSymbolMatches(symbols, "Bar")
	require.Len(t, matches, 1)
	require.Equal(t, "Bar", matches[0].name)
	require.Equal(t, uint32(6), matches[0].rng.Start.Line)
}

func TestFindSymbolMatches_Nested(t *testing.T) {
	t.Parallel()
	method := *docSym("Method", protocol.Method, 10, 14)
	symbols := []protocol.DocumentSymbolResult{
		docSym("MyClass", protocol.Class, 8, 20, method),
	}
	matches := findSymbolMatches(symbols, "Method")
	require.Len(t, matches, 1)
	require.Equal(t, protocol.Method, matches[0].kind)
	require.Equal(t, uint32(10), matches[0].rng.Start.Line)
}

func TestFindSymbolMatches_NotFound(t *testing.T) {
	t.Parallel()
	symbols := []protocol.DocumentSymbolResult{
		docSym("Foo", protocol.Function, 2, 4),
	}
	require.Empty(t, findSymbolMatches(symbols, "Nope"))
}

func TestFindSymbolMatches_Ambiguous(t *testing.T) {
	t.Parallel()
	symbols := []protocol.DocumentSymbolResult{
		docSym("Handle", protocol.Function, 2, 4),
		docSym("Handle", protocol.Method, 10, 14),
	}
	matches := findSymbolMatches(symbols, "Handle")
	require.Len(t, matches, 2)
}

func TestAvailableSymbolsHint(t *testing.T) {
	t.Parallel()
	symbols := []protocol.DocumentSymbolResult{
		docSym("Foo", protocol.Function, 2, 4),
		docSym("Bar", protocol.Function, 6, 8),
	}
	got := availableSymbolsHint(symbols)
	require.Contains(t, got, "Available symbols:")
	require.Contains(t, got, "Foo")
	require.Contains(t, got, "Bar")
	require.Contains(t, got, "line 3")

	require.Contains(t, availableSymbolsHint(nil), "No symbols found")
}

func TestCandidatesHint(t *testing.T) {
	t.Parallel()
	matches := []symbolMatch{
		{name: "Handle", kind: protocol.Function, rng: rng(2, 4)},
		{name: "Handle", kind: protocol.Method, rng: rng(10, 14)},
	}
	got := candidatesHint(matches)
	require.Contains(t, got, "Candidates:")
	require.Contains(t, got, "lines 3-5")
	require.Contains(t, got, "lines 11-15")
}

func TestSymbolStartsAtColumnZero(t *testing.T) {
	t.Parallel()
	require.True(t, symbolStartsAtColumnZero(protocol.Range{
		Start: protocol.Position{Line: 2, Character: 0},
	}))
	require.False(t, symbolStartsAtColumnZero(protocol.Range{
		Start: protocol.Position{Line: 2, Character: 4},
	}))
}

func TestNormalizedEndLine(t *testing.T) {
	t.Parallel()
	// gopls-style: end char non-zero -> unchanged.
	require.Equal(t, 4, normalizedEndLine(protocol.Range{
		Start: protocol.Position{Line: 2},
		End:   protocol.Position{Line: 4, Character: 1},
	}))
	// spec-compliant: end at {nextLine, 0} -> decremented.
	require.Equal(t, 4, normalizedEndLine(protocol.Range{
		Start: protocol.Position{Line: 2},
		End:   protocol.Position{Line: 5, Character: 0},
	}))
	// single-line symbol at {line,0}..{line,0} must not underflow.
	require.Equal(t, 2, normalizedEndLine(protocol.Range{
		Start: protocol.Position{Line: 2},
		End:   protocol.Position{Line: 2, Character: 0},
	}))
}

func TestFormatReplaceSymbolResult(t *testing.T) {
	t.Parallel()
	require.Contains(t, formatReplaceSymbolResult("replace", "Foo", "/a.go", rng(2, 4)), "Replaced symbol 'Foo' in /a.go (lines 3-5)")
	require.Contains(t, formatReplaceSymbolResult("insert_before", "Foo", "/a.go", rng(2, 4)), "before line 3")
	require.Contains(t, formatReplaceSymbolResult("insert_after", "Foo", "/a.go", rng(2, 4)), "after line 5")
	require.Contains(t, formatReplaceSymbolResult("delete", "Foo", "/a.go", rng(2, 4)), "Deleted symbol 'Foo'")

	// End-exclusive range: displayed end line must match the edit (5, not 6).
	exclusive := protocol.Range{
		Start: protocol.Position{Line: 2},
		End:   protocol.Position{Line: 5, Character: 0},
	}
	require.Contains(t, formatReplaceSymbolResult("replace", "Foo", "/a.go", exclusive), "(lines 3-5)")
}
