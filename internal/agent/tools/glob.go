package tools

import (
	"bytes"
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/filepathext"
	"github.com/taigrr/crush/internal/fsext"
	"github.com/taigrr/fantasy"
)

const (
	GlobToolName = "glob"

	globTruncatedNote = "\n\n(Results are truncated. Consider using a more specific path or pattern.)"
	globTimedOutNote  = "\n\n(Search timed out after %s and these results are partial. Narrow the path or use a more specific pattern.)"
)

// globTimeout bounds how long a single glob may run before partial results
// are returned. It is a variable so tests can shorten it.
var globTimeout = 30 * time.Second

// errGlobTimeout is the cancellation cause used for the glob tool's own
// deadline so it can be told apart from a parent-context cancellation.
var errGlobTimeout = errors.New("glob timed out")

//go:embed glob.md.tpl
var globDescriptionTmpl []byte

var globDescriptionTpl = template.Must(
	template.New("globDescription").
		Parse(string(globDescriptionTmpl)),
)

type globDescriptionData struct {
	MaxResults int
}

func globDescription() string {
	return renderTemplate(globDescriptionTpl, globDescriptionData{
		MaxResults: 100,
	})
}

type GlobParams struct {
	Pattern string `json:"pattern" description:"The glob pattern to match files against"`
	Path    string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
}

type GlobResponseMetadata struct {
	NumberOfFiles int  `json:"number_of_files"`
	Truncated     bool `json:"truncated"`
	TimedOut      bool `json:"timed_out,omitempty"`
}

func NewGlobTool(workingDir WorkingDirFunc) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		GlobToolName,
		globDescription(),
		func(ctx context.Context, params GlobParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.Pattern == "" {
				return fantasy.NewTextErrorResponse("pattern is required"), nil
			}

			searchPath := cmp.Or(params.Path, workingDir(ctx))

			timeout := globTimeout
			files, truncated, timedOut, err := globFilesWithTimeout(ctx, params.Pattern, searchPath, 100, timeout)
			if err != nil {
				if ctx.Err() != nil {
					return fantasy.ToolResponse{}, ctx.Err()
				}
				return fantasy.NewTextErrorResponse(fmt.Sprintf("error finding files: %v", err)), nil
			}

			var output string
			if len(files) == 0 {
				output = "No files found"
			} else {
				normalizeFilePaths(files)
				output = strings.Join(files, "\n")
			}
			switch {
			case timedOut:
				output += fmt.Sprintf(globTimedOutNote, timeout)
			case truncated && len(files) > 0:
				output += globTruncatedNote
			}

			return fantasy.WithResponseMetadata(
				fantasy.NewTextResponse(output),
				GlobResponseMetadata{
					NumberOfFiles: len(files),
					Truncated:     truncated,
					TimedOut:      timedOut,
				},
			), nil
		},
	)
}

// globFilesWithTimeout runs globFiles under a bounded deadline. When the
// deadline (and not the parent ctx) fires, the partial matches are returned
// with timedOut set and a nil error. A parent cancellation is surfaced as an
// error so the caller can treat it as a genuine cancellation.
func globFilesWithTimeout(ctx context.Context, pattern, searchPath string, limit int, timeout time.Duration) (files []string, truncated, timedOut bool, err error) {
	globCtx, cancel := context.WithTimeoutCause(ctx, timeout, errGlobTimeout)
	defer cancel()

	files, truncated, err = globFiles(globCtx, pattern, searchPath, limit)
	if err == nil {
		return files, truncated, false, nil
	}
	if ctx.Err() != nil {
		return nil, false, false, ctx.Err()
	}
	if errors.Is(context.Cause(globCtx), errGlobTimeout) {
		return files, truncated, true, nil
	}
	return nil, false, false, err
}

// globFiles finds files matching pattern under searchPath. If ctx is done
// mid-search, the matches collected so far are returned alongside ctx.Err().
func globFiles(ctx context.Context, pattern, searchPath string, limit int) ([]string, bool, error) {
	cmdRg := getRgCmd(ctx, pattern)
	if cmdRg != nil {
		cmdRg.Dir = searchPath
		matches, err := runRipgrep(ctx, cmdRg, searchPath, limit)
		if err == nil {
			return matches, len(matches) >= limit && limit > 0, nil
		}
		if ctx.Err() != nil {
			return matches, len(matches) >= limit && limit > 0, ctx.Err()
		}
		slog.Warn("Ripgrep execution failed, falling back to doublestar", "error", err)
	}

	return fsext.GlobGitignoreAwareContext(ctx, pattern, searchPath, limit)
}

func runRipgrep(ctx context.Context, cmd *exec.Cmd, searchRoot string, limit int) ([]string, error) {
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			// The process was killed by ctx; whatever it printed so far is
			// still useful. Drop a trailing partial path (no NUL terminator).
			if idx := bytes.LastIndexByte(out, 0); idx >= 0 {
				out = out[:idx+1]
			} else {
				out = nil
			}
			return parseRipgrepFiles(out, searchRoot, limit), ctx.Err()
		}
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("ripgrep: %w\n%s", err, out)
	}

	return parseRipgrepFiles(out, searchRoot, limit), nil
}

func parseRipgrepFiles(out []byte, searchRoot string, limit int) []string {
	var matches []string
	for p := range bytes.SplitSeq(out, []byte{0}) {
		if len(p) == 0 {
			continue
		}
		absPath := filepathext.SmartJoin(searchRoot, string(p))
		if fsext.SkipHidden(absPath) {
			continue
		}
		matches = append(matches, absPath)
	}

	slices.SortStableFunc(matches, func(a, b string) int {
		return cmp.Compare(len(a), len(b))
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func normalizeFilePaths(paths []string) {
	for i, p := range paths {
		paths[i] = filepath.ToSlash(p)
	}
}
