package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/fantasy"
)

const (
	FetchToolName = "fetch"
	MaxFetchSize  = 100 * 1024 // 100KB
)

//go:embed fetch.md.tpl
var fetchDescriptionTmpl []byte

var fetchDescriptionTpl = template.Must(
	template.New("fetchDescription").
		Parse(string(fetchDescriptionTmpl)),
)

func NewFetchTool(permissions permission.Service, workingDir WorkingDirFunc, client *http.Client) fantasy.AgentTool {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxIdleConns = 100
		transport.MaxIdleConnsPerHost = 10
		transport.IdleConnTimeout = 90 * time.Second

		client = &http.Client{
			Timeout:   30 * time.Second,
			Transport: transport,
		}
	}

	return fantasy.NewParallelAgentTool(
		FetchToolName,
		FirstLineDescription([]byte(RenderToolDescription(fetchDescriptionTpl))),
		func(ctx context.Context, params FetchParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if params.URL == "" {
				return fantasy.NewTextErrorResponse("URL parameter is required"), nil
			}

			format := strings.ToLower(params.Format)
			if format != "text" && format != "markdown" && format != "html" {
				return fantasy.NewTextErrorResponse("Format must be one of: text, markdown, html"), nil
			}

			if !strings.HasPrefix(params.URL, "http://") && !strings.HasPrefix(params.URL, "https://") {
				return fantasy.NewTextErrorResponse("URL must start with http:// or https://"), nil
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return fantasy.ToolResponse{}, fmt.Errorf("session ID is required for creating a new file")
			}

			p, err := permissions.Request(
				ctx,
				permission.CreatePermissionRequest{
					SessionID:   sessionID,
					Path:        workingDir(ctx),
					ToolCallID:  call.ID,
					ToolName:    FetchToolName,
					Action:      "fetch",
					Description: fmt.Sprintf("Fetch content from URL: %s", params.URL),
					Params:      FetchPermissionsParams(params),
				},
			)
			if err != nil {
				return fantasy.ToolResponse{}, err
			}
			if !p {
				return NewPermissionDeniedResponse(), nil
			}

			// maxFetchTimeoutSeconds is the maximum allowed timeout for fetch requests (2 minutes)
			const maxFetchTimeoutSeconds = 120

			// Handle timeout with context
			requestCtx := ctx
			if params.Timeout > 0 {
				if params.Timeout > maxFetchTimeoutSeconds {
					params.Timeout = maxFetchTimeoutSeconds
				}
				var cancel context.CancelFunc
				requestCtx, cancel = context.WithTimeout(ctx, time.Duration(params.Timeout)*time.Second)
				defer cancel()
			}

			req, err := http.NewRequestWithContext(requestCtx, "GET", params.URL, nil)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to create request: %w", err)
			}

			req.Header.Set("User-Agent", "crush/1.0")

			resp, err := client.Do(req)
			if err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to fetch URL: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("Request failed with status code: %d", resp.StatusCode)), nil
			}

			body, err := io.ReadAll(io.LimitReader(resp.Body, MaxFetchSize))
			if err != nil {
				return fantasy.NewTextErrorResponse("Failed to read response body: " + err.Error()), nil
			}

			content := string(body)

			validUTF8 := utf8.ValidString(content)
			if !validUTF8 {
				return fantasy.NewTextErrorResponse("Response content is not valid UTF-8"), nil
			}
			contentType := resp.Header.Get("Content-Type")

			content, err = formatFetchContent(content, contentType, format)
			if err != nil {
				return fantasy.NewTextErrorResponse(err.Error()), nil
			}

			return fantasy.NewTextResponse(content), nil
		},
	)
}

// formatFetchContent transforms a fetched body into the requested output
// format (text, markdown, or html) and truncates it to MaxFetchSize.
//
// Truncation is applied to the core content before markdown fences are
// added, so the closing fence is never stripped by the size cap.
func formatFetchContent(content, contentType, format string) (string, error) {
	isHTML := strings.Contains(contentType, "text/html")

	switch format {
	case "text":
		if isHTML {
			text, err := extractTextFromHTML(content)
			if err != nil {
				return "", fmt.Errorf("Failed to extract text from HTML: %w", err)
			}
			content = text
		}
		content = truncateFetchContent(content)

	case "markdown":
		if isHTML {
			markdown, err := ConvertHTMLToMarkdown(content)
			if err != nil {
				return "", fmt.Errorf("Failed to convert HTML to Markdown: %w", err)
			}
			content = markdown
		}
		content = "```\n" + truncateFetchContent(content) + "\n```"

	case "html":
		if isHTML {
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
			if err != nil {
				return "", fmt.Errorf("Failed to parse HTML: %w", err)
			}
			body, err := doc.Find("body").Html()
			if err != nil {
				return "", fmt.Errorf("Failed to extract body from HTML: %w", err)
			}
			if body == "" {
				return "", errors.New("No body content found in HTML")
			}
			content = "<html>\n<body>\n" + body + "\n</body>\n</html>"
		}
		content = truncateFetchContent(content)
	}

	return content, nil
}

// truncateFetchContent caps content at MaxFetchSize, appending a notice when
// truncation occurs.
func truncateFetchContent(content string) string {
	if len(content) <= MaxFetchSize {
		return content
	}
	return content[:MaxFetchSize] + fmt.Sprintf("\n\n[Content truncated to %d bytes]", MaxFetchSize)
}

func extractTextFromHTML(html string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}

	text := doc.Find("body").Text()
	text = strings.Join(strings.Fields(text), " ")

	return text, nil
}
