package chat

import "testing"

func TestIsHTMLBlockOpener(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		line string
		want bool
	}{
		// Type 1: script/pre/style/textarea.
		{"script tag", "<script>", true},
		{"pre tag with space", "<pre class=x>", true},
		{"style end of line", "<style", true},
		{"textarea with tab", "<textarea\t", true},
		{"script case-insensitive", "<ScRiPt>", true},
		{"pre but longer name not matched as type1", "<preformatted", true}, // still type 6/7 (starts <letter)
		// Type 2: comment.
		{"html comment", "<!-- hi -->", true},
		// Type 3: processing instruction.
		{"processing instruction", "<?php", true},
		// Type 4: declaration.
		{"declaration", "<!DOCTYPE html>", true},
		{"declaration lowercase letter", "<!x", true},
		{"bang without letter is not type4", "<!1", false},
		// Type 5: CDATA.
		{"cdata", "<![CDATA[stuff", true},
		// Type 6/7: generic open/close tags.
		{"open div", "<div>", true},
		{"close div", "</div>", true},
		{"open tag letter only", "<a", true},
		{"indented up to 3 spaces", "   <div>", true},
		// Negatives.
		{"empty", "", false},
		{"plain text", "hello", false},
		{"less-than digit", "<3", false},
		{"less-than dash", "<-", false},
		{"double less-than", "<<", false},
		{"four spaces indent too deep", "    <div>", false},
		{"lone bracket", "<", false},
		{"close without letter", "</1", false},
		{"midline tag not at start", "x <div>", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isHTMLBlockOpener(tc.line); got != tc.want {
				t.Errorf("isHTMLBlockOpener(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}
