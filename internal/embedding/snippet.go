package embedding

import "strings"

const snippetWindow = 80 // chars of context on each side of a match

// snippet extracts a single-line window around the first case-insensitive
// occurrence of query in text. If query is absent (e.g. a semantic-only
// hit) it returns the head of the text. Newlines are collapsed so the
// snippet renders on one line.
func snippet(text, query string) string {
	idx := -1
	if query != "" {
		idx = strings.Index(strings.ToLower(text), strings.ToLower(query))
	}
	if idx < 0 {
		body := text
		suffix := ""
		if len(body) > 2*snippetWindow {
			body = body[:2*snippetWindow]
			suffix = "…"
		}
		return strings.ReplaceAll(body, "\n", " ") + suffix
	}
	start := max(idx-snippetWindow, 0)
	end := min(idx+len(query)+snippetWindow, len(text))
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(text) {
		suffix = "…"
	}
	return prefix + strings.ReplaceAll(text[start:end], "\n", " ") + suffix
}
