package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/headless"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Agent skill for driving Crush from other coding agents",
	Long: `Agent skill that teaches another coding agent (Claude Code, clawdbot,
Codex, ...) when and how to hand work to Crush through 'crush mcp' or the
headless CLI ('crush run -o json', 'crush session wait|answer').`,
}

var skillPrintCmd = &cobra.Command{
	Use:   "print",
	Short: "Print the agent skill (SKILL.md) to stdout",
	Long: `Print the agent skill as a SKILL.md file. Save it wherever your agent
loads skills from, or paste it into its instructions.`,
	Example: `
crush skill print
crush skill print > SKILL.md`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), headless.AgentSkill)
		return err
	},
}

func init() {
	skillCmd.AddCommand(skillPrintCmd)
	rootCmd.AddCommand(skillCmd)
}
