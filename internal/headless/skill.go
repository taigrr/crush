package headless

import (
	_ "embed"
	"strings"
)

//go:embed agent_skill.md
var agentSkill string

// AgentSkill is a skill file that teaches another coding agent when and
// how to hand work to Crush through `crush mcp` or the headless CLI. It
// is printed by `crush skill print`. Line endings are normalized so a
// Windows checkout (core.autocrlf) prints the same file.
var AgentSkill = strings.ReplaceAll(agentSkill, "\r\n", "\n")
