#!/usr/bin/env bash
# Runs the prompt/checkpoint cache microbenchmarks on a baseline ref (with
# uncached shims) and on the working tree, then compares with benchstat.
set -euo pipefail
BASE=${BASE:-f32dc805}
COUNT=${COUNT:-10}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
OUT=$ROOT/bench/out/attach-final
export GOFLAGS=-p=2 GOCACHE=${GOCACHE:-$HOME/Library/Caches/go-build-swarm} GOMAXPROCS=4
mkdir -p "$OUT"
WT=$(mktemp -d)/base
git -C "$ROOT" worktree add -q --detach "$WT" "$BASE"
trap 'git -C "$ROOT" worktree remove --force "$WT"' EXIT
cp "$ROOT"/internal/agent/prompt/cache_bench_test.go "$WT"/internal/agent/prompt/
cp "$ROOT"/internal/checkpoint/ignorecache_bench_test.go "$WT"/internal/checkpoint/
cat > "$WT"/internal/agent/prompt/cache_benchhelper_test.go <<'G'
package prompt

import (
	"context"
	"os/exec"
	"testing"

	"github.com/taigrr/crush/internal/skills"
)

func expirePromptInputCaches() {}

func cachedGitStatus(ctx context.Context, dir string) string { return getGitStatus(ctx, dir) }

func cachedDiscoverSkills(paths []string) []*skills.Skill { return skills.Discover(paths) }

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
G
uptime | tee "$OUT/load.txt"
for i in $(seq "$COUNT"); do
  (cd "$WT" && go test -run '^$' -bench . -benchmem -count=1 ./internal/agent/prompt ./internal/checkpoint) >> "$OUT/micro-base.txt"
  (cd "$ROOT" && go test -run '^$' -bench . -benchmem -count=1 ./internal/agent/prompt ./internal/checkpoint) >> "$OUT/micro-final.txt"
done
uptime | tee -a "$OUT/load.txt"
~/go/bin/benchstat "$OUT/micro-base.txt" "$OUT/micro-final.txt" | tee "$OUT/micro-benchstat.txt"
