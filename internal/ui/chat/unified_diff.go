package chat

import (
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/diffdetect"
	"github.com/taigrr/crush/internal/ui/common"
	"github.com/taigrr/crush/internal/ui/styles"
)

type parsedDiffFile struct {
	path   string
	before string
	after  string
}

func looksLikeDiff(content string) bool {
	return diffdetect.IsUnifiedDiff(content)
}

// diffFileParser accumulates parsed files from a unified diff stream. It is a
// small state machine: header lines select/create the current file, and
// content lines (+/-/space) are appended to the current file's before/after
// buffers.
type diffFileParser struct {
	files      []diffFileBuilder
	currentIdx int
	inHunk     bool
}

type diffFileBuilder struct {
	path   string
	before strings.Builder
	after  strings.Builder
}

// diffHeaderPath strips the "--- "/"+++ " marker, the "a/"/"b/" prefix, and any
// trailing tab-delimited timestamp from a file header line.
func diffHeaderPath(line, marker, abPrefix string) string {
	p := strings.TrimPrefix(line, marker)
	p = strings.TrimPrefix(p, abPrefix)
	if idx := strings.Index(p, "\t"); idx >= 0 {
		p = p[:idx]
	}
	return p
}

func (dp *diffFileParser) startFile(path string) {
	dp.files = append(dp.files, diffFileBuilder{path: path})
	dp.currentIdx = len(dp.files) - 1
}

// handleHeader processes a line that may be a file/hunk header. It returns true
// when the line was consumed as a header (and should not be treated as
// content). lines/i give lookahead for the "--- " then "+++ " plain-diff case.
func (dp *diffFileParser) handleHeader(line string, lines []string, i int) bool {
	switch {
	case strings.HasPrefix(line, "diff --git "):
		dp.inHunk = false
		parts := strings.SplitN(line, " ", 4)
		if len(parts) >= 4 {
			dp.startFile(strings.TrimPrefix(parts[3], "b/"))
		}
		return true

	case strings.HasPrefix(line, "@@"):
		dp.inHunk = true
		return true

	case strings.HasPrefix(line, "index "), strings.HasPrefix(line, "new file"), strings.HasPrefix(line, "deleted file"):
		dp.inHunk = false
		return true
	}

	nextIsPlusHeader := i+1 < len(lines) && strings.HasPrefix(lines[i+1], "+++ ")
	if strings.HasPrefix(line, "--- ") && (!dp.inHunk || nextIsPlusHeader) {
		startedNewFileFromHunk := dp.inHunk && nextIsPlusHeader
		dp.inHunk = false
		p := diffHeaderPath(line, "--- ", "a/")
		if dp.currentIdx < 0 || startedNewFileFromHunk {
			dp.startFile(p)
			return true
		}
		if p != "/dev/null" {
			dp.files[dp.currentIdx].path = p
		}
		return true
	}

	if strings.HasPrefix(line, "+++ ") && !dp.inHunk {
		p := diffHeaderPath(line, "+++ ", "b/")
		if dp.currentIdx < 0 {
			if p != "/dev/null" {
				dp.startFile(p)
			}
			return true
		}
		cur := dp.files[dp.currentIdx].path
		if p != "/dev/null" && (cur == "" || strings.HasPrefix(cur, "/dev/null")) {
			dp.files[dp.currentIdx].path = p
		}
		return true
	}

	return false
}

// handleContent appends a +/-/space content line to the current file. Lines
// with no current file are ignored.
func (dp *diffFileParser) handleContent(line string) {
	if dp.currentIdx < 0 {
		return
	}
	switch {
	case strings.HasPrefix(line, "-"):
		dp.inHunk = true
		dp.files[dp.currentIdx].before.WriteString(line[1:])
		dp.files[dp.currentIdx].before.WriteByte('\n')
	case strings.HasPrefix(line, "+"):
		dp.inHunk = true
		dp.files[dp.currentIdx].after.WriteString(line[1:])
		dp.files[dp.currentIdx].after.WriteByte('\n')
	case strings.HasPrefix(line, " "):
		dp.inHunk = true
		content := line[1:]
		dp.files[dp.currentIdx].before.WriteString(content)
		dp.files[dp.currentIdx].before.WriteByte('\n')
		dp.files[dp.currentIdx].after.WriteString(content)
		dp.files[dp.currentIdx].after.WriteByte('\n')
	}
}

func parseUnifiedDiff(content string) []parsedDiffFile {
	dp := &diffFileParser{currentIdx: -1}
	lines := strings.Split(content, "\n")

	for i, line := range lines {
		if dp.handleHeader(line, lines, i) {
			continue
		}
		dp.handleContent(line)
	}

	result := make([]parsedDiffFile, 0, len(dp.files))
	for _, f := range dp.files {
		result = append(result, parsedDiffFile{
			path:   f.path,
			before: strings.TrimSuffix(f.before.String(), "\n"),
			after:  strings.TrimSuffix(f.after.String(), "\n"),
		})
	}
	return result
}

func toolOutputDiffContentFromUnified(sty *styles.Styles, content string, width int, expanded bool) string {
	files := parseUnifiedDiff(content)
	if len(files) == 0 {
		bodyWidth := width - toolBodyLeftPaddingTotal
		return sty.Tool.Body.Render(toolOutputCodeContent(sty, "result.diff", content, 0, bodyWidth, expanded))
	}
	bodyWidth := width - toolBodyLeftPaddingTotal
	var blocks []string
	for i, f := range files {
		formatter := common.DiffFormatter(sty).
			Before(f.path, f.before).
			After(f.path, f.after).
			Width(bodyWidth)
		if len(files) > 1 {
			formatter = formatter.FileName(f.path)
		}
		if width > maxTextWidth {
			formatter = formatter.Split()
		}
		formatted := formatter.String()
		if i < len(files)-1 {
			formatted += "\n"
		}
		blocks = append(blocks, formatted)
	}
	combined := strings.Join(blocks, "\n")
	lines := strings.Split(combined, "\n")
	maxLines := responseContextHeight
	if expanded {
		maxLines = len(lines)
	}
	if len(lines) > maxLines && !expanded {
		truncMsg := sty.Tool.DiffTruncation.
			Width(bodyWidth).
			Render(fmt.Sprintf(assistantMessageTruncateFormat, len(lines)-maxLines))
		combined = strings.Join(lines[:maxLines], "\n") + "\n" + truncMsg
	}
	return sty.Tool.Body.Render(combined)
}
