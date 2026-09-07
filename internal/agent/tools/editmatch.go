package tools

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Snippet rendering knobs shared by edit and multiedit responses.
const (
	editSnippetContext  = 3
	editSnippetMaxLines = 80
	editSnippetHeadTail = 30
	editMaxOccurrences  = 5
	editMaxCandidates   = 2
	editCandidateMinSim = 0.3
)

// lineRange is a 1-based inclusive span of lines.
type lineRange struct {
	start, end int
}

// editMatch is a single located occurrence of old_string in a file, with
// the replacement text adjusted to fit the file when the match was fuzzy.
type editMatch struct {
	start, end int
	fuzzy      bool
	newString  string
	note       string
}

// locateEdit finds the unique region of content that old_string refers to.
//
// It first tries an exact match. If that fails it retries comparing lines
// with leading/trailing whitespace stripped and internal runs collapsed; a
// unique fuzzy hit is accepted and new_string is re-indented to match the
// file where that can be done safely. Any failure returns an error whose
// text already carries a numbered view of the region(s) the model most
// likely meant, so the next attempt can be one-shot.
func locateEdit(content, oldString, newString string) (editMatch, error) {
	if oldString == "" {
		return editMatch{}, errors.New("old_string cannot be empty for content replacement")
	}

	first := strings.Index(content, oldString)
	if first != -1 {
		last := strings.LastIndex(content, oldString)
		if first != last {
			return editMatch{}, multipleMatchesError(content, oldString)
		}
		return editMatch{start: first, end: first + len(oldString), newString: newString}, nil
	}

	if m, ok, err := locateNormalized(content, oldString, newString); err != nil {
		return editMatch{}, err
	} else if ok {
		return m, nil
	}

	return editMatch{}, notFoundError(content, oldString)
}

// applyEdit performs one find-and-replace on content and returns the new
// content, the replaced spans in the new content, and an optional note
// describing any fuzzy matching that took place.
func applyEdit(content, oldString, newString string, replaceAll bool) (string, []lineRange, string, error) {
	if replaceAll {
		newContent, regions, err := applyReplaceAll(content, oldString, newString)
		return newContent, regions, "", err
	}
	m, err := locateEdit(content, oldString, newString)
	if err != nil {
		return "", nil, "", err
	}
	newContent, region := applyMatch(content, m)
	return newContent, []lineRange{region}, m.note, nil
}

// editSuccessText builds the model-facing text for a successful edit: the
// summary line, any fuzzy-match note, and a numbered view of the edited
// region(s) so the model can verify the result without a follow-up view.
func editSuccessText(summary, newContent string, regions []lineRange, note string) string {
	var sb strings.Builder
	sb.WriteString(summary)
	if note != "" {
		sb.WriteString("\n")
		sb.WriteString(note)
	}
	if len(regions) == 0 {
		return sb.String()
	}
	shown := regions
	if len(shown) > editMaxOccurrences {
		shown = shown[:editMaxOccurrences]
	}
	fmt.Fprintf(&sb, "\nUpdated %s", describeRegions(shown))
	if len(regions) > len(shown) {
		fmt.Fprintf(&sb, " (and %d more)", len(regions)-len(shown))
	}
	sb.WriteString(":\n")
	sb.WriteString(renderRegions(newContent, shown, editSnippetContext))
	return sb.String()
}

// applyMatch splices the replacement into content and reports the span of
// the replacement in the resulting text.
func applyMatch(content string, m editMatch) (string, lineRange) {
	newContent := content[:m.start] + m.newString + content[m.end:]
	startLine := 1 + strings.Count(content[:m.start], "\n")
	endLine := startLine + spanLines(m.newString) - 1
	if m.newString == "" {
		endLine = startLine
	}
	return newContent, lineRange{startLine, endLine}
}

// applyReplaceAll replaces every exact occurrence of old_string and reports
// the spans of the replacements in the resulting text.
func applyReplaceAll(content, oldString, newString string) (string, []lineRange, error) {
	if oldString == "" {
		return "", nil, errors.New("old_string cannot be empty for content replacement")
	}
	if !strings.Contains(content, oldString) {
		return "", nil, notFoundError(content, oldString)
	}

	var sb strings.Builder
	var regions []lineRange
	rest := content
	for {
		idx := strings.Index(rest, oldString)
		if idx == -1 {
			sb.WriteString(rest)
			break
		}
		sb.WriteString(rest[:idx])
		startLine := 1 + strings.Count(sb.String(), "\n")
		sb.WriteString(newString)
		endLine := startLine + spanLines(newString) - 1
		regions = append(regions, lineRange{startLine, endLine})
		rest = rest[idx+len(oldString):]
	}
	return sb.String(), regions, nil
}

// shiftRegions adjusts previously recorded regions for an edit that changed
// the line count at edited, then appends edited. Regions are kept sorted and
// merged when they touch.
func shiftRegions(regions []lineRange, edited lineRange, oldLineCount int) []lineRange {
	delta := (edited.end - edited.start + 1) - oldLineCount
	out := make([]lineRange, 0, len(regions)+1)
	for _, r := range regions {
		if r.start > edited.start {
			r.start += delta
			r.end += delta
		}
		out = append(out, r)
	}
	out = append(out, edited)
	return mergeRegions(out)
}

// spanLines is the number of lines a piece of text occupies, treating a
// trailing newline as a terminator rather than the start of another line.
func spanLines(s string) int {
	if s == "" {
		return 1
	}
	return strings.Count(strings.TrimSuffix(s, "\n"), "\n") + 1
}

func dedupeRegions(regions []lineRange) []lineRange {
	var out []lineRange
	for _, r := range regions {
		if len(out) > 0 && out[len(out)-1] == r {
			continue
		}
		out = append(out, r)
	}
	return out
}

func mergeRegions(regions []lineRange) []lineRange {
	if len(regions) == 0 {
		return regions
	}
	sort.Slice(regions, func(i, j int) bool { return regions[i].start < regions[j].start })
	merged := []lineRange{regions[0]}
	for _, r := range regions[1:] {
		top := &merged[len(merged)-1]
		if r.start <= top.end+1 {
			top.end = max(top.end, r.end)
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// renderRegions returns numbered views of each region (plus context lines)
// of content, in the same format the view tool uses.
func renderRegions(content string, regions []lineRange, context int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)

	padded := make([]lineRange, 0, len(regions))
	for _, r := range regions {
		padded = append(padded, lineRange{max(1, r.start-context), min(total, r.end+context)})
	}
	padded = mergeRegions(padded)

	var sb strings.Builder
	for i, r := range padded {
		if r.start > r.end {
			continue
		}
		if i > 0 {
			sb.WriteString("\n...\n")
		}
		sb.WriteString(renderLines(lines, r))
	}
	return sb.String()
}

func renderLines(lines []string, r lineRange) string {
	count := r.end - r.start + 1
	if count <= editSnippetMaxLines {
		return addLineNumbers(strings.Join(lines[r.start-1:r.end], "\n"), r.start)
	}
	head := lineRange{r.start, r.start + editSnippetHeadTail - 1}
	tail := lineRange{r.end - editSnippetHeadTail + 1, r.end}
	omitted := tail.start - head.end - 1
	return addLineNumbers(strings.Join(lines[head.start-1:head.end], "\n"), head.start) +
		fmt.Sprintf("\n... (%d lines omitted) ...\n", omitted) +
		addLineNumbers(strings.Join(lines[tail.start-1:tail.end], "\n"), tail.start)
}

func describeRegions(regions []lineRange) string {
	parts := make([]string, 0, len(regions))
	for _, r := range regions {
		if r.start == r.end {
			parts = append(parts, fmt.Sprintf("line %d", r.start))
		} else {
			parts = append(parts, fmt.Sprintf("lines %d-%d", r.start, r.end))
		}
	}
	return strings.Join(parts, ", ")
}

func multipleMatchesError(content, oldString string) error {
	var regions []lineRange
	offset := 0
	total := 0
	for {
		idx := strings.Index(content[offset:], oldString)
		if idx == -1 {
			break
		}
		total++
		abs := offset + idx
		if len(regions) < editMaxOccurrences {
			start := 1 + strings.Count(content[:abs], "\n")
			regions = append(regions, lineRange{start, start + spanLines(oldString) - 1})
		}
		offset = abs + max(1, len(oldString))
		if offset >= len(content) {
			break
		}
	}

	shown := dedupeRegions(regions)
	var sb strings.Builder
	fmt.Fprintf(&sb, "old_string appears multiple times in the file (%d occurrences at %s", total, describeRegions(shown))
	if total > len(regions) {
		fmt.Fprintf(&sb, ", and %d more", total-len(regions))
	}
	sb.WriteString("). Include more surrounding context so it matches exactly once, or set replace_all to true. Occurrences:\n")
	sb.WriteString(renderRegions(content, shown, 2))
	return errors.New(sb.String())
}

func notFoundError(content, oldString string) error {
	candidates := closestRegions(content, oldString, editMaxCandidates)
	if len(candidates) == 0 {
		return errors.New("old_string not found in file. Make sure it matches exactly, including whitespace and line breaks.")
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "old_string not found in file. Closest match is at %s; the file actually contains:\n", describeRegions(candidates))
	sb.WriteString(renderRegions(content, candidates, editSnippetContext))
	sb.WriteString("\nRe-issue the edit using the exact text shown above as old_string.")
	return errors.New(sb.String())
}

// normalizeLine collapses whitespace so that indentation and trailing
// space differences do not prevent a line match.
func normalizeLine(line string) string {
	return strings.Join(strings.Fields(line), " ")
}

// lineOffsets returns the byte offset at which each line of content starts.
func lineOffsets(content string) []int {
	offsets := []int{0}
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			offsets = append(offsets, i+1)
		}
	}
	return offsets
}

// locateNormalized matches old_string against whole file lines with
// whitespace normalized. It only succeeds on a unique hit.
func locateNormalized(content, oldString, newString string) (editMatch, bool, error) {
	oldTrimmed := strings.TrimSuffix(oldString, "\n")
	oldLines := editLines(oldTrimmed)
	normOld := make([]string, len(oldLines))
	allBlank := true
	for i, l := range oldLines {
		normOld[i] = normalizeLine(l)
		if normOld[i] != "" {
			allBlank = false
		}
	}
	if allBlank {
		return editMatch{}, false, nil
	}

	fileLines := editLines(content)
	normFile := make([]string, len(fileLines))
	for i, l := range fileLines {
		normFile[i] = normalizeLine(l)
	}

	n := len(normOld)
	var hits []int
	for i := 0; i+n <= len(normFile); i++ {
		match := true
		for j := range n {
			if normFile[i+j] != normOld[j] {
				match = false
				break
			}
		}
		if match {
			hits = append(hits, i)
			if len(hits) > editMaxOccurrences {
				break
			}
		}
	}
	if len(hits) == 0 {
		return editMatch{}, false, nil
	}
	if len(hits) > 1 {
		regions := make([]lineRange, 0, len(hits))
		for _, h := range hits {
			regions = append(regions, lineRange{h + 1, h + n})
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "old_string (ignoring whitespace differences) matches %d regions in the file (%s). Include more surrounding context so it matches exactly once. Occurrences:\n", len(hits), describeRegions(regions))
		sb.WriteString(renderRegions(content, regions, 2))
		return editMatch{}, false, errors.New(sb.String())
	}

	hit := hits[0]
	offsets := lineOffsets(content)
	start := offsets[hit]
	end := offsets[hit+n-1] + len(fileLines[hit+n-1])
	if strings.HasSuffix(oldString, "\n") && end < len(content) && content[end] == '\n' {
		end++
	}

	matched := fileLines[hit : hit+n]
	adjusted, how := reindent(oldLines, matched, newString)
	note := fmt.Sprintf("Note: old_string matched lines %d-%d only after ignoring whitespace differences; %s.", hit+1, hit+n, how)

	return editMatch{start: start, end: end, fuzzy: true, newString: adjusted, note: note}, true, nil
}

func leadingWhitespace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// reindent rewrites the indentation of newString so it lines up with the
// file when old_string was consistently over- or under-indented, or used
// spaces where the file uses tabs (and vice versa). It returns the adjusted
// text and a short description of what was done.
func reindent(oldLines, fileLines []string, newString string) (string, string) {
	type pair struct{ old, file string }
	var pairs []pair
	for i := range oldLines {
		if strings.TrimSpace(oldLines[i]) == "" || strings.TrimSpace(fileLines[i]) == "" {
			continue
		}
		pairs = append(pairs, pair{leadingWhitespace(oldLines[i]), leadingWhitespace(fileLines[i])})
	}
	if len(pairs) == 0 {
		return newString, "new_string applied verbatim"
	}

	same := true
	for _, p := range pairs {
		if p.old != p.file {
			same = false
			break
		}
	}
	if same {
		return newString, "new_string applied verbatim"
	}

	if extra, ok := consistentPrefix(pairs, func(p pair) (string, string) { return p.old, p.file }); ok && extra != "" {
		return mapIndent(newString, func(ws string) string { return strings.TrimPrefix(ws, extra) }),
			fmt.Sprintf("new_string was de-indented by %q to match the file", extra)
	}
	if extra, ok := consistentPrefix(pairs, func(p pair) (string, string) { return p.file, p.old }); ok && extra != "" {
		return mapIndent(newString, func(ws string) string { return extra + ws }),
			fmt.Sprintf("new_string was indented by %q to match the file", extra)
	}

	if width, ok := tabWidth(pairs, func(p pair) (string, string) { return p.old, p.file }); ok {
		spaces := strings.Repeat(" ", width)
		return mapIndent(newString, func(ws string) string { return strings.ReplaceAll(ws, spaces, "\t") }),
			fmt.Sprintf("new_string indentation was converted from %d-space to tabs to match the file", width)
	}
	if width, ok := tabWidth(pairs, func(p pair) (string, string) { return p.file, p.old }); ok {
		spaces := strings.Repeat(" ", width)
		return mapIndent(newString, func(ws string) string { return strings.ReplaceAll(ws, "\t", spaces) }),
			fmt.Sprintf("new_string indentation was converted from tabs to %d-space to match the file", width)
	}

	return newString, "new_string applied verbatim, verify its indentation"
}

// consistentPrefix reports the prefix p such that longer == p + shorter for
// every pair, if one exists.
func consistentPrefix[T any](pairs []T, pick func(T) (longer, shorter string)) (string, bool) {
	prefix := ""
	for i, p := range pairs {
		longer, shorter := pick(p)
		if !strings.HasSuffix(longer, shorter) {
			return "", false
		}
		got := longer[:len(longer)-len(shorter)]
		if i == 0 {
			prefix = got
		} else if got != prefix {
			return "", false
		}
	}
	return prefix, true
}

// tabWidth reports the space width w such that every spaced indentation is
// exactly w spaces per tab of the tabbed indentation.
func tabWidth[T any](pairs []T, pick func(T) (spaced, tabbed string)) (int, bool) {
	width := 0
	for _, p := range pairs {
		spaced, tabbed := pick(p)
		if strings.Contains(spaced, "\t") || strings.Contains(tabbed, " ") {
			return 0, false
		}
		if len(tabbed) == 0 {
			if len(spaced) != 0 {
				return 0, false
			}
			continue
		}
		if len(spaced)%len(tabbed) != 0 {
			return 0, false
		}
		w := len(spaced) / len(tabbed)
		if width == 0 {
			width = w
		} else if w != width {
			return 0, false
		}
	}
	return width, width > 0
}

func mapIndent(s string, fn func(string) string) string {
	lines := editLines(s)
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ws := leadingWhitespace(l)
		lines[i] = fn(ws) + l[len(ws):]
	}
	return strings.Join(lines, "\n")
}

// closestRegions scores every window of the file against old_string by
// normalized-line equality and token overlap, returning the best
// non-overlapping regions above a minimum similarity.
func closestRegions(content, oldString string, limit int) []lineRange {
	oldLines := editLines(strings.TrimSuffix(oldString, "\n"))
	normOld := make([]string, 0, len(oldLines))
	for _, l := range oldLines {
		if n := normalizeLine(l); n != "" {
			normOld = append(normOld, n)
		}
	}
	if len(normOld) == 0 {
		return nil
	}
	oldTokens := make([]map[string]struct{}, len(normOld))
	for i, l := range normOld {
		oldTokens[i] = tokenSet(l)
	}

	fileLines := editLines(content)
	normFile := make([]string, len(fileLines))
	fileTokens := make([]map[string]struct{}, len(fileLines))
	for i, l := range fileLines {
		normFile[i] = normalizeLine(l)
		fileTokens[i] = tokenSet(normFile[i])
	}

	n := len(normOld)
	if n > len(fileLines) {
		n = len(fileLines)
	}
	type scored struct {
		start int
		score float64
	}
	var windows []scored
	for i := 0; i+n <= len(fileLines); i++ {
		score := 0.0
		for j := range n {
			if normFile[i+j] == normOld[j] {
				score++
				continue
			}
			score += jaccard(oldTokens[j], fileTokens[i+j])
		}
		if score >= editCandidateMinSim*float64(n) {
			windows = append(windows, scored{i, score})
		}
	}
	if len(windows) == 0 {
		return nil
	}
	sort.SliceStable(windows, func(a, b int) bool { return windows[a].score > windows[b].score })

	var regions []lineRange
	for _, w := range windows {
		if len(regions) == limit {
			break
		}
		if w.score < windows[0].score*0.75 {
			break
		}
		r := lineRange{w.start + 1, w.start + n}
		overlaps := false
		for _, existing := range regions {
			if r.start <= existing.end && existing.start <= r.end {
				overlaps = true
				break
			}
		}
		if !overlaps {
			regions = append(regions, r)
		}
	}
	return mergeRegions(regions)
}

func tokenSet(line string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, tok := range strings.FieldsFunc(line, func(r rune) bool {
		return !(r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) {
		set[tok] = struct{}{}
	}
	return set
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	return float64(inter) / float64(union)
}

func editLines(s string) []string {
	return strings.Split(s, "\n")
}

// snippetRegions decides whether a successful edit should echo the edited
// region back. The model already holds the surrounding context when it has
// viewed the file this session and the match was exact, so the snippet is
// only worth its tokens when the file was never viewed or the match was
// fuzzy (and new_string may have been re-indented).
func snippetRegions(regions []lineRange, previouslyRead bool, note string) []lineRange {
	if previouslyRead && note == "" {
		return nil
	}
	return regions
}
