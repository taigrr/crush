package chat

import "testing"

func TestIsLinkRefDefinition(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"basic", "[label]: https://example.com", true},
		{"tab after colon", "[x]:\thttps://e.com", true},
		{"up to 3 spaces indent", "   [x]: dest", true},
		{"four spaces indent too deep", "    [x]: dest", false},
		{"no leading bracket", "label]: dest", false},
		{"empty label", "[]: dest", false},
		{"no closing bracket", "[label: dest", false},
		{"no colon", "[label] dest", false},
		{"colon but no destination", "[label]:", false},
		{"colon whitespace only no destination", "[label]:   ", false},
		{"empty line", "", false},
		{"label with spaces", "[my label]: dest", true},
		{"first close bracket must be immediately followed by colon", "[a]b]: dest", false},
		{"multiple whitespace before dest", "[x]:     dest", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isLinkRefDefinition(tc.line); got != tc.want {
				t.Errorf("isLinkRefDefinition(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}
