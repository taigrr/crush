#!/usr/bin/env python3
"""Tabulate bench/scale.sh output: bench/scale/summary.py [outdir]."""
import glob, json, os, re, sys
out = sys.argv[1] if len(sys.argv) > 1 else "bench/out/scale"
print("| mode | N ws | M sess | ready ms (med +-sd) | overviews req ms | srv list_overviews ms | probe overview ms (med) | payload MB | sessions returned | unavailable | RSS MB before/after | disk MB |")
print("|---|---|---|---|---|---|---|---|---|---|---|---|")
rows = []
for f in glob.glob(f"{out}/reuse*-*-*.json"):
    m = re.match(r".*/(reuse(?:-teardown)?)-(\d+)-(\d+)\.json", f)
    rows.append((m.group(1), int(m.group(2)), int(m.group(3)), f))
for mode, n, s, f in sorted(rows):
    d = json.load(open(f)); ph = d["phases"]; p = d["fixture"].get("probe", {})
    req = ph["overviews_req_done"]["median"] - ph["overviews_req_start"]["median"]
    sp = d.get("spans", {})
    lo = next((v for k, v in sp.items() if "srv.list_overviews" in k), None)
    lo = lo["median_total"] if lo else None
    lat = sorted(p.get("lat_ms", [0])); disk = open(f"{out}/disk-{n}-{s}.txt").read().strip()
    print(f"| {mode} | {n} | {s} | {ph['ready']['median']:.0f} +-{ph['ready']['stddev']:.0f} | {req:.0f} | {lo if lo is None else round(lo,1)} | {lat[len(lat)//2]:.0f} | {p.get('bytes',0)/1e6:.1f} | {p.get('sessions')} | {p.get('unavailable')} | {p.get('rss_kb_before',0)/1024:.0f}/{p.get('rss_kb_after',0)/1024:.0f} | {int(disk)//1024} |")
