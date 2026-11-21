#!/usr/bin/env python3
"""Pool bench/out/attach-final e2e raw runs per binary/fixture/mode and
print first-launch and 2..N medians/stddev for key phases and srv spans."""
import glob, json, os, statistics, sys
d = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), "out", "attach-final")
KEYS = ["first_frame", "ready", "workspace_created", "overviews_loaded"]
SPANS = ["srv|span|srv.prompt.git_status", "srv|span|srv.prompt.skills_discover", "srv|span|srv.prompt.build",
         "srv|span|srv.checkpoint.read_gitignore_patterns"]
def stat(v):
    return (statistics.median(v), statistics.stdev(v) if len(v) > 1 else 0.0, len(v)) if v else None
for fx in ("small", "large"):
    for mode in ("reuse", "reuse-teardown"):
        print(f"\n## {fx} / {mode}")
        for bin in ("base", "final"):
            runs, firsts = [], []
            for f in sorted(glob.glob(f"{d}/e2e-{bin}-{fx}-{mode}-r*.raw.json")):
                j = json.load(open(f))
                runs += j["runs"]
                if j.get("warmup"):
                    firsts.append(j["warmup"])
            row = [bin]
            for k in KEYS:
                s = stat([r[k] for r in runs if k in r])
                row.append(f"{k}={s[0]:.1f}±{s[1]:.1f}(n={s[2]})" if s else f"{k}=-")
            fs = stat([r["ready"] for r in firsts if "ready" in r])
            row.append(f"first_ready={fs[0]:.1f}(n={fs[2]})" if fs else "")
            for sp in SPANS:
                vals = [r.get("spans", {}).get(sp, {}).get("sum", 0) / 1e6 for r in runs]
                ns = [r.get("spans", {}).get(sp, {}).get("n", 0) for r in runs]
                row.append(f"{sp.split('.')[-1]}={statistics.median(vals):.2f}ms/{statistics.median(ns):.0f}calls")
            print(" | ".join(row))
