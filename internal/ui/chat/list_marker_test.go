package chat

import "testing"

func TestIsListItemMarker(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"empty", "", false},
		{"dash space", "- item", true},
		{"star space", "* item", true},
		{"plus space", "+ item", true},
		{"dash tab", "-\titem", true},
		{"dash no space", "-item", false},
		{"dash alone", "-", false},
		{"ordered dot space", "1. item", true},
		{"ordered paren space", "1) item", true},
		{"multi-digit ordered", "123. item", true},
		{"ordered no space", "1.item", false},
		{"ordered no delimiter", "1 item", false},
		{"too many digits", "1234567890. item", false},
		{"nine digits ok", "123456789. x", true},
		{"digits then eol", "12", false},
		{"digit dot eol", "1.", false},
		{"plain text", "hello", false},
		{"dash tab only no content still marker", "- ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isListItemMarker(tc.line); got != tc.want {
				t.Errorf("isListItemMarker(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestIsSetextUnderlineCandidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"empty", "", false},
		{"equals", "===", true},
		{"dashes", "---", true},
		{"single equals", "=", true},
		{"single dash", "-", true},
		{"trailing whitespace", "===  ", true},
		{"leading whitespace allowed", "  ===", true},
		{"mixed not allowed", "=-=", false},
		{"other char", "~~~", false},
		{"whitespace only", "   ", false},
		{"equals then text", "=== text", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSetextUnderlineCandidate(tc.line); got != tc.want {
				t.Errorf("isSetextUnderlineCandidate(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}
