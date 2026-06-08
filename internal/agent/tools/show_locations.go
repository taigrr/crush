package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/taigrr/fantasy"

	"github.com/taigrr/crush/internal/editor"
)

const ShowLocationsToolName = "show_locations"

//go:embed show_locations.md
var showLocationsDescription string

// ShowLocationsParams matches the shape neocrush.nvim's picker expects.
type ShowLocationsParams struct {
	Title string              `json:"title,omitempty" description:"Optional picker title"`
	Items []ShowLocationsItem `json:"items" description:"List of locations to display"`
}

// ShowLocationsItem is a single picker entry.
type ShowLocationsItem struct {
	Filename string `json:"filename" description:"Absolute or workspace-relative path"`
	Line     int    `json:"lnum" description:"1-indexed line number"`
	Col      int    `json:"col,omitempty" description:"1-indexed column (default 1)"`
	Text     string `json:"text" description:"Code snippet at this location"`
	Note     string `json:"note" description:"Why this location is relevant; shown in the explanation pane"`
	Type     string `json:"type,omitempty" description:"N=note (default), I=info, W=warning, E=error"`
}

// NewShowLocationsTool returns the show_locations tool. The editor
// bridge is resolved per-turn from ctx (WithEditorBridge).
func NewShowLocationsTool() fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ShowLocationsToolName,
		showLocationsDescription,
		func(ctx context.Context, params ShowLocationsParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if len(params.Items) == 0 {
				return fantasy.NewTextErrorResponse("show_locations requires at least one item"), nil
			}
			bridge := EditorBridgeFromContext(ctx)
			items := make([]editor.Location, len(params.Items))
			for i, it := range params.Items {
				// Neovim's picker expects 1-indexed line/column. The model
				// often omits these (defaulting to 0), so clamp to 1 to keep
				// the entries valid and avoid nil/zero rendering errors.
				line := max(it.Line, 1)
				col := max(it.Col, 1)
				items[i] = editor.Location{
					Filename: it.Filename,
					Line:     line,
					Col:      col,
					Text:     it.Text,
					Note:     it.Note,
					Type:     it.Type,
				}
			}
			if err := bridge.ShowLocations(ctx, params.Title, items); err != nil {
				if errors.Is(err, editor.ErrUnavailable) {
					return fantasy.NewTextErrorResponse("Editor bridge is not available; no editor is attached."), nil
				}
				return fantasy.NewTextErrorResponse(fmt.Sprintf("show_locations failed: %v", err)), nil
			}
			return fantasy.NewTextResponse(fmt.Sprintf("Displayed %d location(s) in editor.", len(items))), nil
		},
	)
}
