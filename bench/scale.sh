#!/usr/bin/env bash
# Scale baseline: workspaces x sessions matrix against a private server.
# Usage: bench/scale.sh <crush-binary> [outdir] [runs]
# Env: SCALE_WS="10 100 300" SCALE_SESS="200 2000 5000" SCALE_TMP=/Volumes/Store1/tmp
set -euo pipefail
BIN=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
OUT=${2:-bench/out/scale}
RUNS=${3:-10}
TMPBASE=${SCALE_TMP:-/Volumes/Store1/tmp}
HERE=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$OUT" "$TMPBASE"
GEN="$TMPBASE/scalegen"
(cd "$HERE/.." && go build -o "$GEN" ./bench/scale/gen)
uptime > "$OUT/load.txt"
for ws in ${SCALE_WS:-10 100 300}; do
  for ss in ${SCALE_SESS:-200 2000 5000}; do
    root=$(mktemp -d "$TMPBASE/crush-scale-XXXXXX")
    "$GEN" -root "$root" -workspaces "$ws" -sessions "$ss" -clone 2>/dev/null | tee "$OUT/gen-$ws-$ss.txt"
    du -sk "$root" | awk '{print $1}' > "$OUT/disk-$ws-$ss.txt"
    for mode in reuse reuse-teardown; do
      python3 "$HERE/startup.py" "$BIN" "$RUNS" --mode "$mode" --fixture-root "$root" \
        --probe 1 --json "$OUT/$mode-$ws-$ss.json" > "$OUT/$mode-$ws-$ss.txt" 2>&1 || echo "FAILED $mode $ws $ss"
    done
    rm -rf "$root"
    uptime >> "$OUT/load.txt"
  done
done
