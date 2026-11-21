package prompt

import (
	"os/exec"
	"testing"
)

func expirePromptInputCaches() {
	gitStatusCache.reset()
	userSkillCache.reset()
}

func benchGit(b *testing.B, dir string, args ...string) {
	b.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(cmd.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		b.Fatal(err, string(out))
	}
}
