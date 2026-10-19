package headless

import _ "embed"

// AgentSkill is a skill file that teaches another coding agent when and
// how to hand work to Crush through `crush mcp` or the headless CLI. It
// is printed by `crush skill print`.
//
//go:embed agent_skill.md
var AgentSkill string
