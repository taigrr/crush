#!/usr/bin/env bash
# Compare two bench/run.sh results. Usage: bench/compare.sh <old> <new>
set -euo pipefail

OLD="${1:?usage: bench/compare.sh <old-label> <new-label>}"
NEW="${2:?usage: bench/compare.sh <old-label> <new-label>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/bench/out"
BENCHSTAT="${BENCHSTAT:-$HOME/go/bin/benchstat}"

echo "== go benchmarks: $OLD vs $NEW"
"$BENCHSTAT" "$OLD=$OUT/$OLD/bench.txt" "$NEW=$OUT/$NEW/bench.txt"

echo
echo "== hyperfine medians (ms)"
python3 - "$OUT/$OLD/hyperfine.json" "$OUT/$NEW/hyperfine.json" "$OLD" "$NEW" <<'EOF'
import json, sys
def stats(r):
    # hyperfine >= 2 nests timings under summary.time_wall_clock.
    s = r.get("summary", {}).get("time_wall_clock", r)
    return s["median"] * 1e3, s["stddev"] * 1e3

def name(r):
    return r.get("name") or r["command"]

old, new = (json.load(open(p))["results"] for p in sys.argv[1:3])
o = {name(r): r for r in old}
print(f"{'command':<10} {sys.argv[3]:>14} {sys.argv[4]:>14} {'delta':>8}")
for r in new:
    n = name(r)
    b, s = stats(r)
    if n in o:
        a, sa = stats(o[n])
        print(f"{n:<10} {a:8.2f}±{sa:5.2f} {b:8.2f}±{s:5.2f} {(b - a) / a * 100:+7.1f}%")
    else:
        print(f"{n:<10} {'-':>14} {b:8.2f}±{s:5.2f}")
EOF
