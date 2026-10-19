package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/headless"
)

func TestSkillPrint(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	skillPrintCmd.SetOut(&out)
	require.NoError(t, skillPrintCmd.RunE(skillPrintCmd, nil))
	require.Equal(t, headless.AgentSkill, out.String())
	require.True(t, strings.HasPrefix(out.String(), "---\nname: crush\n"), "the output is a valid SKILL.md with frontmatter")
	require.Contains(t, out.String(), "crush_start")
	require.Contains(t, out.String(), "crush session wait")
}
