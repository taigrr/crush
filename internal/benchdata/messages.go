// Package benchdata generates deterministic synthetic data for benchmarks.
package benchdata

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/message"
)

// Markdown is a representative assistant reply with lists, a table, a code
// block, a quote and links.
const Markdown = `Here is the plan for the **refactor**:

1. Extract the ` + "`loader`" + ` into its own package.
2. Add a cache keyed by path.
3. Wire it into the coordinator.

| Step | Owner | Status |
|------|-------|--------|
| one  | alice | done   |
| two  | bob   | wip    |

` + "```go" + `
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
` + "```" + `

> Note: the cache must be invalidated when the file changes.

- bullet one with a [link](https://example.com)
- bullet two with *emphasis*
`

// Messages returns n deterministic messages for sessionID cycling through
// user prompts, assistant markdown replies, assistant tool calls and tool
// results.
func Messages(sessionID string, n int) []message.Message {
	msgs := make([]message.Message, 0, n)
	for i := range n {
		id := fmt.Sprintf("msg-%06d", i)
		created := int64(1_700_000_000 + i)
		msg := message.Message{
			ID:        id,
			SessionID: sessionID,
			Model:     "bench-model",
			Provider:  "bench",
			CreatedAt: created,
			UpdatedAt: created,
		}
		switch i % 4 {
		case 0:
			msg.Role = message.User
			msg.Parts = []message.ContentPart{
				message.TextContent{Text: fmt.Sprintf("Please refactor module %d and explain the changes. %s", i, strings.Repeat("Some more context. ", 8))},
			}
		case 1:
			msg.Role = message.Assistant
			msg.Parts = []message.ContentPart{
				message.ReasoningContent{Thinking: "Thinking about the refactor " + strings.Repeat("step ", 20)},
				message.TextContent{Text: fmt.Sprintf("### Reply %d\n\n%s", i, Markdown)},
				message.Finish{Reason: message.FinishReasonEndTurn, Time: created},
			}
		case 2:
			toolID := fmt.Sprintf("call-%06d", i)
			input, _ := json.Marshal(map[string]any{
				"command":     fmt.Sprintf("go test ./internal/pkg%d/...", i),
				"description": "Run tests",
			})
			msg.Role = message.Assistant
			msg.Parts = []message.ContentPart{
				message.TextContent{Text: "Running the tests."},
				message.ToolCall{ID: toolID, Name: "bash", Input: string(input), Finished: true},
				message.Finish{Reason: message.FinishReasonToolUse, Time: created},
			}
		case 3:
			toolID := fmt.Sprintf("call-%06d", i-1)
			var out strings.Builder
			for j := range 30 {
				fmt.Fprintf(&out, "ok  \tgithub.com/example/pkg%d/sub%d\t0.%03ds\n", i, j, j)
			}
			msg.Role = message.Tool
			msg.Parts = []message.ContentPart{
				message.ToolResult{ToolCallID: toolID, Name: "bash", Content: out.String()},
			}
		}
		msgs = append(msgs, msg)
	}
	return msgs
}
