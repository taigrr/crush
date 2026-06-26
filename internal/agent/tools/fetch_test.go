package tools

import (
	"strings"
	"testing"
)

func TestFormatFetchContent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		content     string
		contentType string
		format      string
		want        string
	}{
		{
			name:        "text from html extracts body text",
			content:     "<html><body><p>Hello  world</p></body></html>",
			contentType: "text/html; charset=utf-8",
			format:      "text",
			want:        "Hello world",
		},
		{
			name:        "text from non-html passes through",
			content:     "plain text",
			contentType: "text/plain",
			format:      "text",
			want:        "plain text",
		},
		{
			name:        "markdown wraps non-html in fences",
			content:     "raw data",
			contentType: "text/plain",
			format:      "markdown",
			want:        "```\nraw data\n```",
		},
		{
			name:        "html extracts body",
			content:     "<html><head><title>t</title></head><body><p>hi</p></body></html>",
			contentType: "text/html",
			format:      "html",
			want:        "<html>\n<body>\n<p>hi</p>\n</body>\n</html>",
		},
		{
			name:        "html non-html passes through",
			content:     "not html",
			contentType: "application/json",
			format:      "html",
			want:        "not html",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := formatFetchContent(tc.content, tc.contentType, tc.format)
			if err != nil {
				t.Fatalf("formatFetchContent error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFormatFetchContentMarkdownTruncationKeepsFence verifies the regression
// fix: oversized markdown content is truncated before being wrapped, so the
// closing fence is preserved rather than chopped off by the size cap.
func TestFormatFetchContentMarkdownTruncationKeepsFence(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", MaxFetchSize+100)
	got, err := formatFetchContent(big, "text/plain", "markdown")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if !strings.HasPrefix(got, "```\n") {
		t.Fatalf("missing opening fence: %q", got[:8])
	}
	if !strings.HasSuffix(got, "\n```") {
		t.Fatalf("closing fence was stripped by truncation")
	}
	if !strings.Contains(got, "[Content truncated to") {
		t.Fatal("expected truncation notice")
	}
}

func TestTruncateFetchContent(t *testing.T) {
	t.Parallel()

	short := "small"
	if got := truncateFetchContent(short); got != short {
		t.Fatalf("short content changed: %q", got)
	}

	big := strings.Repeat("a", MaxFetchSize+10)
	got := truncateFetchContent(big)
	if !strings.HasPrefix(got, strings.Repeat("a", MaxFetchSize)) {
		t.Fatal("truncated content prefix mismatch")
	}
	if !strings.Contains(got, "[Content truncated to") {
		t.Fatal("missing truncation notice")
	}

	// Exactly MaxFetchSize is not truncated.
	exact := strings.Repeat("b", MaxFetchSize)
	if got := truncateFetchContent(exact); strings.Contains(got, "truncated") {
		t.Fatal("content of exactly MaxFetchSize should not be truncated")
	}
}
