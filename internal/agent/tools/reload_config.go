package tools

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/permission"
	"github.com/taigrr/fantasy"
)

const ReloadConfigToolName = "reload_config"

//go:embed reload_config.md
var reloadConfigDescription string

type ReloadConfigParams struct{}

// NewReloadConfigTool returns a tool that reloads Crush's config from
// disk. It is gated behind sysadmin mode: when sysadmin mode is off the
// tool refuses to run and instructs the user to enable it.
func NewReloadConfigTool(cfg *config.ConfigStore, permissions permission.Service) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		ReloadConfigToolName,
		reloadConfigDescription,
		func(ctx context.Context, _ ReloadConfigParams, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			if !permissions.SysadminMode() {
				return fantasy.NewTextErrorResponse(
					"reload_config requires Sysadmin Mode. Ask the user to enable it from the command palette (\"Enable Sysadmin Mode\"), then retry.",
				), nil
			}

			before := cfg.ConfigStaleness()
			if err := cfg.ReloadFromDisk(ctx); err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to reload config: %s", err)), nil
			}

			var b strings.Builder
			b.WriteString("Config reloaded from disk.\n")
			if len(before.Changed) > 0 {
				fmt.Fprintf(&b, "changed = %s\n", strings.Join(before.Changed, ", "))
			}
			if len(before.Missing) > 0 {
				fmt.Fprintf(&b, "missing = %s\n", strings.Join(before.Missing, ", "))
			}
			if !before.Dirty {
				b.WriteString("note = no on-disk changes were detected; reloaded anyway\n")
			}
			return fantasy.NewTextResponse(b.String()), nil
		},
	)
}
