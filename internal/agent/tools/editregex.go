package tools

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// editSpan is a replaced region in the new content together with the
// number of lines the matched text occupied in the old content, which
// multiedit needs to shift previously recorded regions correctly.
type editSpan struct {
	region   lineRange
	oldLines int
}

// applyRegexEdit performs a regex find-and-replace on content. The pattern
// uses Go RE2 syntax and replacement supports $1 / ${name} expansion. When
// replaceAll is false the pattern must match exactly once; otherwise every
// match is replaced.
func applyRegexEdit(content, pattern, replacement string, replaceAll bool) (string, []editSpan, error) {
	if pattern == "" {
		return "", nil, errors.New("old_string (regex pattern) cannot be empty")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", nil, fmt.Errorf("invalid regex: %w (Go RE2 syntax; lookahead/lookbehind are not supported — use a capture group and $1 instead)", err)
	}

	idxs := re.FindAllStringSubmatchIndex(content, -1)
	if len(idxs) == 0 {
		return "", nil, fmt.Errorf("regex %q matched nothing in the file", pattern)
	}
	if !replaceAll && len(idxs) > 1 {
		return "", nil, regexMultipleMatchesError(content, pattern, idxs)
	}

	var sb strings.Builder
	var spans []editSpan
	last := 0
	for _, loc := range idxs {
		start, end := loc[0], loc[1]
		sb.WriteString(content[last:start])
		startLine := 1 + strings.Count(sb.String(), "\n")
		expanded := re.ExpandString(nil, replacement, content, loc)
		sb.Write(expanded)
		endLine := startLine + spanLines(string(expanded)) - 1
		spans = append(spans, editSpan{
			region:   lineRange{startLine, endLine},
			oldLines: spanLines(content[start:end]),
		})
		last = end
	}
	sb.WriteString(content[last:])

	newContent := sb.String()
	if newContent == content {
		return "", nil, errors.New("regex matched but the replacement produced identical content")
	}
	return newContent, spans, nil
}

func regexMultipleMatchesError(content, pattern string, idxs [][]int) error {
	var regions []lineRange
	for _, loc := range idxs {
		if len(regions) >= editMaxOccurrences {
			break
		}
		start := 1 + strings.Count(content[:loc[0]], "\n")
		regions = append(regions, lineRange{start, start + spanLines(content[loc[0]:loc[1]]) - 1})
	}
	shown := dedupeRegions(regions)
	var sb strings.Builder
	fmt.Fprintf(&sb, "regex %q matches %d times (at %s", pattern, len(idxs), describeRegions(shown))
	if len(idxs) > len(regions) {
		fmt.Fprintf(&sb, ", and %d more", len(idxs)-len(regions))
	}
	sb.WriteString("). Set replace_all=true to replace every match, or anchor the pattern so it matches once:\n")
	sb.WriteString(renderRegions(content, shown, editSnippetContext))
	return errors.New(sb.String())
}

// spanRegions projects the regions out of a slice of spans.
func spanRegions(spans []editSpan) []lineRange {
	out := make([]lineRange, 0, len(spans))
	for _, s := range spans {
		out = append(out, s.region)
	}
	return out
}
