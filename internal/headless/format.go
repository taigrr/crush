package headless

import (
	"fmt"
	"strings"
)

// OutputFormat selects how a headless command writes its output.
type OutputFormat string

const (
	// FormatText writes the final assistant text, unchanged from the
	// historical `crush run` behavior.
	FormatText OutputFormat = "text"
	// FormatJSON writes a single [Result] object once the run ends.
	FormatJSON OutputFormat = "json"
	// FormatStreamJSON writes one JSON event per line as the run
	// progresses, terminated by a [Result] line.
	FormatStreamJSON OutputFormat = "stream-json"
)

// OutputFormats lists every accepted format in display order.
var OutputFormats = []OutputFormat{FormatText, FormatJSON, FormatStreamJSON}

// ParseOutputFormat validates s and returns the matching format. The
// empty string selects [FormatText].
func ParseOutputFormat(s string) (OutputFormat, error) {
	if s == "" {
		return FormatText, nil
	}
	for _, f := range OutputFormats {
		if strings.EqualFold(s, string(f)) {
			return f, nil
		}
	}
	return "", fmt.Errorf("invalid output format %q (want one of %s)", s, formatList())
}

// IsStructured reports whether the format emits JSON.
func (f OutputFormat) IsStructured() bool {
	return f == FormatJSON || f == FormatStreamJSON
}

func formatList() string {
	names := make([]string, len(OutputFormats))
	for i, f := range OutputFormats {
		names[i] = string(f)
	}
	return strings.Join(names, ", ")
}
