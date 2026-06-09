package styles_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// hexLiteral matches a 6-digit hex color string like "#1e1e2e".
var hexLiteral = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// TestNoRawColorsInThemedUI enforces that the themed TUI never hardcodes
// colors. Every color in the TUI must flow through the styles package (the
// theme registry / quickStyle palette) so user themes can recolor it.
//
// Scope: the whole TUI tree under internal/ui, except:
//   - internal/ui/styles: the theme definitions themselves (the one place
//     allowed to reference charmtone and define palettes).
//   - internal/ui/diffview: a standalone library with its own default
//     styles; Crush always overrides these via the theme's s.Diff.
//
// CLI command output (internal/cmd) is intentionally out of scope: it runs
// outside the themed TUI and styles one-off command output directly.
func TestNoRawColorsInThemedUI(t *testing.T) {
	t.Parallel()

	// Walk up to the repo root, then into internal/ui.
	uiRoot, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)
	uiRoot = filepath.Join(uiRoot, "ui")

	skipDirs := map[string]bool{
		filepath.Join(uiRoot, "styles"):   true,
		filepath.Join(uiRoot, "diffview"): true,
	}

	var violations []string

	err = filepath.WalkDir(uiRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		dir := filepath.Dir(path)
		for skip := range skipDirs {
			if dir == skip || strings.HasPrefix(dir, skip+string(filepath.Separator)) {
				return nil
			}
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind == token.STRING {
					v := strings.Trim(x.Value, "`\"")
					if hexLiteral.MatchString(v) {
						pos := fset.Position(x.Pos())
						violations = append(violations,
							pos.String()+": raw hex color "+x.Value)
					}
				}
			case *ast.SelectorExpr:
				if ident, ok := x.X.(*ast.Ident); ok && ident.Name == "charmtone" {
					pos := fset.Position(x.Pos())
					violations = append(violations,
						pos.String()+": raw charmtone."+x.Sel.Name)
				}
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)

	require.Empty(t, violations,
		"themed UI must use theme tokens, not raw colors:\n%s",
		strings.Join(violations, "\n"))
}
