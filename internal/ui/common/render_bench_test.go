package common

import (
	"fmt"
	"strings"
	"testing"

	"github.com/taigrr/crush/internal/benchdata"
)

// BenchmarkMarkdownRender measures glamour rendering of a representative
// assistant reply through the shared cached renderer.
func BenchmarkMarkdownRender(b *testing.B) {
	sty := DefaultCommon(nil).Styles
	renderer := MarkdownRenderer(sty, 120)
	doc := strings.Repeat(benchdata.Markdown+"\n", 4)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := renderer.Render(doc); err != nil {
			b.Fatal(err)
		}
	}
}

func benchGoFile(lines int, edited bool) string {
	var sb strings.Builder
	sb.WriteString("package bench\n\nimport \"fmt\"\n\n")
	for i := range lines {
		if edited && i%10 == 0 {
			fmt.Fprintf(&sb, "func fn%d(x int) int { return x * %d } // changed\n", i, i+1)
			continue
		}
		fmt.Fprintf(&sb, "func fn%d(x int) int { return x + %d }\n", i, i)
	}
	return sb.String()
}

// BenchmarkDiffRender measures syntax-highlighted diff rendering of a 400
// line Go file with 40 edited lines, as shown for edit/write tool results.
func BenchmarkDiffRender(b *testing.B) {
	sty := DefaultCommon(nil).Styles
	before, after := benchGoFile(400, false), benchGoFile(400, true)
	for _, mode := range []string{"unified", "split"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				dv := DiffFormatter(sty).Before("bench.go", before).After("bench.go", after).Width(180)
				if mode == "split" {
					dv = dv.Split()
				} else {
					dv = dv.Unified()
				}
				_ = dv.String()
			}
		})
	}
}
