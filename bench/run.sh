#!/usr/bin/env bash
# Run the Crush performance harness and store raw results under
# bench/out/<label>/. Usage: bench/run.sh <label> [-quick]
set -euo pipefail

LABEL="${1:?usage: bench/run.sh <label> [-quick]}"
QUICK="${2:-}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/bench/out/$LABEL"
HYPERFINE="${HYPERFINE:-/opt/homebrew/bin/hyperfine}"
COUNT="${BENCH_COUNT:-10}"
BENCHTIME="${BENCH_TIME:-1s}"
if [[ "$QUICK" == "-quick" ]]; then
	COUNT=1
	BENCHTIME=100ms
fi

export GOFLAGS="${GOFLAGS:--p=2}"
export GOCACHE="${GOCACHE:-$HOME/Library/Caches/go-build-swarm}"
export GOMAXPROCS=4

# Go benchmarks, by package. Keep in sync with bench/README.md.
BENCH_PKGS=(
	./internal/ui/model
	./internal/ui/common
	./internal/message
	./internal/config
	./internal/server
	./internal/pubsub
	./internal/cluster
	./internal/replica
)
BENCH_RE='^(BenchmarkUIView|BenchmarkUIDraw|BenchmarkUIScrollFrame|BenchmarkUISessionLoad|BenchmarkMarkdownRender|BenchmarkDiffRender|BenchmarkMessageList|BenchmarkLoadFromConfigPaths|BenchmarkWireFullMessage|BenchmarkWireDeltas|BenchmarkBrokerPublish|BenchmarkCluster.*|BenchmarkReplica.*)$'

mkdir -p "$OUT"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

record_env() {
	{
		echo "## $1 $(date -u +%Y-%m-%dT%H:%M:%SZ)"
		echo "uptime: $(uptime)"
		echo "go: $(go version)"
		echo "git: $(git -C "$ROOT" rev-parse HEAD) $(git -C "$ROOT" status --porcelain | wc -l | tr -d ' ') dirty files"
		echo "GOMAXPROCS=$GOMAXPROCS count=$COUNT benchtime=$BENCHTIME"
	} >>"$OUT/env.txt"
}

: >"$OUT/env.txt"
record_env start

echo "==> building crush"
BIN="$TMP/crush"
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN" .)

# Isolate the CLI from the user's real config, data and daemon.
export HOME="$TMP/home"
export XDG_CONFIG_HOME="$TMP/config"
export XDG_DATA_HOME="$TMP/data"
export XDG_CACHE_HOME="$TMP/cache"
export CRUSH_GLOBAL_CONFIG="$TMP/config/crush"
export CRUSH_GLOBAL_DATA="$TMP/data/crush"
export CRUSH_DISABLE_METRICS=1
export DO_NOT_TRACK=1
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME"

echo "==> hyperfine"
RUNS=30
[[ "$QUICK" == "-quick" ]] && RUNS=5
"$HYPERFINE" --warmup 3 --runs "$RUNS" -N \
	--export-json "$OUT/hyperfine.json" \
	--command-name version "$BIN --version" \
	--command-name help "$BIN --help" \
	--command-name dirs "$BIN dirs" | tee "$OUT/hyperfine.txt"

echo "==> interactive startup (cold + warm server)"
SRUNS=15
[[ "$QUICK" == "-quick" ]] && SRUNS=3
python3 "$ROOT/bench/startup.py" "$BIN" "$SRUNS" --json "$OUT/startup-cold.json" | tee "$OUT/startup-cold.txt"
python3 "$ROOT/bench/startup.py" "$BIN" "$SRUNS" --warm --json "$OUT/startup-warm.json" | tee "$OUT/startup-warm.txt"

echo "==> go test -bench (count=$COUNT)"
(cd "$ROOT" && go test -run '^$' -bench "$BENCH_RE" -benchmem \
	-count="$COUNT" -benchtime="$BENCHTIME" -timeout 60m "${BENCH_PKGS[@]}") |
	tee "$OUT/bench.txt"

record_env end
echo "==> results in $OUT"
