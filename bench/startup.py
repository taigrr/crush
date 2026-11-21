#!/usr/bin/env python3
"""Measure interactive Crush startup: exec -> first frame -> ready.

Usage:
  bench/startup.py <crush-binary> [runs] [--mode MODE] [--fixture KIND]
                   [--json out.json] [--raw raw.json]

Modes (default cold):
  cold            fresh auto-spawned server AND fresh temp project per run.
  warm-new        (legacy --warm) one auto-spawned server, but a NEW temp
                  project every run. Every run therefore pays full
                  workspace creation (config, DB create+migrate, skills,
                  app.New); it does NOT measure re-attaching to a loaded
                  workspace.
  reuse           one explicitly started persistent server and the SAME
                  project path every run. The killed client's workspace
                  stays loaded for the reconnect grace, so runs 2..N hit
                  the adopt-existing-workspace path.
  reuse-teardown  like reuse, but the server's reconnect grace is 0 so the
                  workspace is torn down after every client exits; runs
                  2..N rebuild the same workspace on a live server (what a
                  user sees relaunching the TUI after quitting).

Fixtures (default small):
  small   empty git project.
  large   git monorepo (~10k files incl. nested gitignored node_modules),
          project commands and skills, plus 8 extra registered projects
          with 200-session databases (exercises cross-project peeks).

HOME/XDG/CRUSH_GLOBAL_* are isolated in a temp dir, the server socket is
private, the mobile/device listeners are disabled, and a fake
openai-compat provider pointing at a closed local port makes the
workspace "configured" without network access. Phase timestamps come
from CRUSH_STARTUP_TRACE; spans/counts (every occurrence) are summarized
per role (cli = TUI process, srv = server process) and per run. The
first run of every mode is a discarded warmup. Every process that wrote
to the trace is killed afterwards.
"""

import json
import os
import shutil
import signal
import statistics
import subprocess
import sys
import tempfile
import time

PHASES = ["main", "server_ready", "workspace_created", "agent_init",
          "program_start", "first_frame", "overviews_req_start",
          "overviews_req_done", "overviews_loaded"]
MODES = ("cold", "warm-new", "reuse", "reuse-teardown")
FAKE_PROVIDER = {
    "providers": {"fake": {
        "type": "openai-compat", "base_url": "http://127.0.0.1:9/v1",
        "api_key": "bench",
        "models": [{"id": "m", "name": "m", "context_window": 8000,
                    "default_max_tokens": 1000}],
    }},
    "models": {"large": {"model": "m", "provider": "fake"},
               "small": {"model": "m", "provider": "fake"}},
}


def read_trace(path):
    """Return (marks, spans, pids). spans: list of (pid, kind, label, v)."""
    marks, spans, pids = {}, [], set()
    try:
        with open(path) as f:
            for line in f:
                parts = line.split()
                if len(parts) == 3:
                    pid, phase, ns = int(parts[0]), parts[1], int(parts[2])
                    pids.add(pid)
                    marks.setdefault((pid, phase), ns)
                elif len(parts) == 5 and parts[1] in ("span", "count"):
                    pid = int(parts[0])
                    pids.add(pid)
                    spans.append((pid, parts[1], parts[2], int(parts[4])))
    except FileNotFoundError:
        pass
    return marks, spans, pids


def kill(pids):
    for pid in pids:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass


def base_env(root, trace):
    env = dict(os.environ)
    env.update({
        "HOME": f"{root}/home", "XDG_CONFIG_HOME": f"{root}/config",
        "XDG_DATA_HOME": f"{root}/data", "XDG_CACHE_HOME": f"{root}/cache",
        "XDG_STATE_HOME": f"{root}/state",
        "CRUSH_GLOBAL_CONFIG": f"{root}/config/crush",
        "CRUSH_GLOBAL_DATA": f"{root}/data/crush",
        "CRUSH_DISABLE_METRICS": "1", "DO_NOT_TRACK": "1",
        "CRUSH_DISABLE_PROVIDER_AUTO_UPDATE": "1",
        "CRUSH_STARTUP_TRACE": trace, "TERM": "xterm-256color",
        "COLUMNS": "120", "LINES": "40",
    })
    return env


def write(path, text):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w") as f:
        f.write(text)


def git_init(path):
    os.makedirs(path, exist_ok=True)
    subprocess.run(["git", "init", "-q", path], check=True,
                   env={"PATH": os.environ["PATH"], "HOME": path})


def make_large(proj):
    git_init(proj)
    write(f"{proj}/.gitignore", "node_modules/\ndist/\n")
    write(f"{proj}/AGENTS.md", "# bench\n")
    n = 0
    for p in range(50):
        for i in range(130):
            write(f"{proj}/packages/pkg{p}/src/m{i // 20}/f{i}.ts",
                  "export const x = 1\n")
            n += 1
        for d in range(4):
            for i in range(10):
                write(f"{proj}/packages/pkg{p}/node_modules/dep{d}/lib/i{i}.js",
                      "module.exports = 1\n")
                n += 1
    for d in range(30):
        for i in range(20):
            write(f"{proj}/node_modules/top{d}/i{i}.js", "1\n")
            n += 1
    for i in range(20):
        write(f"{proj}/.crush/commands/c{i}.md", f"command {i}\n")
    for i in range(10):
        write(f"{proj}/.agents/skills/s{i}/SKILL.md",
              f"---\nname: s{i}\ndescription: bench skill {i}\n---\nbody\n")
    # Commit the tracked files so git status reads the index like a real
    # checkout instead of hashing thousands of untracked files.
    genv = {"PATH": os.environ["PATH"], "HOME": proj,
            "GIT_AUTHOR_NAME": "b", "GIT_AUTHOR_EMAIL": "b@b",
            "GIT_COMMITTER_NAME": "b", "GIT_COMMITTER_EMAIL": "b@b"}
    subprocess.run(["git", "-C", proj, "add", "-A"], check=True, env=genv)
    subprocess.run(["git", "-C", proj, "commit", "-qm", "fixture"],
                   check=True, env=genv)
    return n


def seed_sessions(db, count):
    now = int(time.time())
    rows = ",".join(
        f"('bench-{i}', 'session {i}', {now - i}, {now - i})"
        for i in range(count))
    subprocess.run(["sqlite3", db,
                    f"INSERT INTO sessions (id, title, updated_at, created_at) VALUES {rows};"],
                   check=True)


class Server:
    """An explicitly started persistent server on a private socket."""

    def __init__(self, binary, root, sock, trace, grace=None):
        env = base_env(root, trace)
        if grace is not None:
            env["CRUSH_STARTUP_TRACE_RECONNECT_GRACE"] = grace
        self.proc = subprocess.Popen(
            [binary, "-H", f"unix://{sock}", "server", "--persistent",
             "--client-addr", "", "--client-ws-addr", ""],
            env=env, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL, start_new_session=True)
        deadline = time.time() + 15
        while not os.path.exists(sock):
            if time.time() > deadline or self.proc.poll() is not None:
                raise SystemExit("server failed to start")
            time.sleep(0.01)
        time.sleep(0.1)

    def stop(self):
        try:
            os.killpg(self.proc.pid, signal.SIGINT)
            self.proc.wait(timeout=10)
        except (ProcessLookupError, subprocess.TimeoutExpired):
            kill([self.proc.pid])
            self.proc.wait()


def run_once(binary, root, sock, trace, proj, settle=0.4, timeout=30.0):
    env = base_env(root, trace)
    if os.path.exists(trace):
        os.unlink(trace)
    t0 = time.time_ns()
    proc = subprocess.Popen(
        ["script", "-q", "/dev/null", binary, "-H", f"unix://{sock}",
         "-c", proj],
        env=env, stdin=subprocess.PIPE, stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL, start_new_session=True)
    client = None
    result = None
    deadline = time.time() + timeout
    while time.time() < deadline:
        marks, _, _ = read_trace(trace)
        for (pid, phase) in marks:
            if phase == "program_start":
                client = pid
        if client is not None:
            ff = marks.get((client, "first_frame"))
            ov = marks.get((client, "overviews_loaded"))
            if ff and ov:
                result = {p: (marks[(client, p)] - t0) / 1e6
                          for p in PHASES if (client, p) in marks}
                result["ready"] = (max(ff, ov) - t0) / 1e6
                for (pid, phase), ns in marks.items():
                    if pid != client:
                        result["srv." + phase] = (ns - t0) / 1e6
                break
        time.sleep(0.002)
    if result is not None:
        # Let async client/server work started by the attach (command
        # loading, MCP init, swarm backfill) finish and record its spans.
        time.sleep(settle)
        _, spans, _ = read_trace(trace)
        agg = {}
        for pid, kind, label, v in spans:
            role = "cli" if pid == client else "srv"
            key = f"{role}|{kind}|{label}"
            a = agg.setdefault(key, {"n": 0, "sum": 0})
            a["n"] += 1
            a["sum"] += v
        result["spans"] = agg
    try:
        os.killpg(proc.pid, signal.SIGKILL)
    except (ProcessLookupError, PermissionError):
        pass
    proc.wait()
    if client:
        kill([client])
    return result


def setup_fixture(binary, root, kind):
    if kind == "scale":
        # Prebuilt by bench/scale/gen (config, registry and DBs in place).
        return f"{root}/work/ws0000", {"fixture": "scale", "root": root}
    write(f"{root}/config/crush/crush.json", json.dumps(FAKE_PROVIDER))
    proj = f"{root}/work/mono"
    info = {"fixture": kind}
    if kind == "small":
        git_init(proj)
        return proj, info
    info["files"] = make_large(proj)
    # Register 8 extra projects with populated session DBs by attaching
    # each once on a throwaway zero-grace server, then seeding rows.
    sock = f"{root}/setup.sock"
    trace = f"{root}/setup-trace.txt"
    srv = Server(binary, root, sock, trace, grace="0s")
    extras = []
    try:
        for i in range(8):
            p = f"{root}/work/extra{i}"
            git_init(p)
            if run_once(binary, root, sock, trace, p, settle=0.2) is None:
                raise SystemExit(f"fixture attach {i} timed out")
            extras.append(p)
        time.sleep(0.5)
    finally:
        srv.stop()
    for p in extras:
        seed_sessions(f"{p}/.crush/crush.db", 200)
    info["extra_projects"] = len(extras)
    info["sessions_per_extra"] = 200
    return proj, info


def rss_kb(pid):
    out = subprocess.run(["ps", "-o", "rss=", "-p", str(pid)],
                         capture_output=True, text=True).stdout.strip()
    return int(out) if out else 0


def probe_server(sock, pid, runs=5):
    """Fetch /v1/workspace-overviews over the private socket: latency,
    payload size, and server RSS before/after (peeks touch every DB)."""
    import http.client
    import socket

    class UConn(http.client.HTTPConnection):
        def connect(self):
            self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            self.sock.connect(sock)

    res = {"rss_kb_before": rss_kb(pid), "lat_ms": []}
    for _ in range(runs):
        c = UConn("localhost", timeout=120)
        t0 = time.time()
        c.request("GET", "/v1/workspace-overviews")
        r = c.getresponse()
        body = r.read()
        res["lat_ms"].append(round((time.time() - t0) * 1000, 1))
        res["status"] = r.status
        res["bytes"] = len(body)
        try:
            data = json.loads(body)
            res["workspaces"] = len(data)
            res["sessions"] = sum(len(w.get("sessions") or []) for w in data)
            res["unavailable"] = sum(1 for w in data if w.get("unavailable"))
            errs = [w.get("error") for w in data if w.get("error")]
            res["first_error"] = errs[0] if errs else ""
        except ValueError:
            pass
        c.close()
    res["rss_kb_after"] = rss_kb(pid)
    return res


def summarize(results, mode):
    keys = PHASES + ["ready"] + sorted(
        {k for r in results for k in r if k.startswith("srv.")})
    summary = {"phases": {}, "spans": {}}
    print(f"{'phase':<22}{'median ms':>12}{'stddev':>10}  (n={len(results)}, {mode})")
    for k in keys:
        vals = [r[k] for r in results if k in r]
        if not vals:
            continue
        med = statistics.median(vals)
        sd = statistics.stdev(vals) if len(vals) > 1 else 0.0
        summary["phases"][k] = {"median": med, "stddev": sd, "values": vals}
        print(f"{k:<22}{med:>12.1f}{sd:>10.1f}")
    labels = sorted({k for r in results for k in r.get("spans", {})})
    print(f"\n{'role|kind|label':<58}{'calls/run':>10}{'median':>10}")
    for k in labels:
        ns = [r["spans"].get(k, {"n": 0})["n"] for r in results]
        sums = [r["spans"][k]["sum"] for r in results if k in r["spans"]]
        if "|span|" in k:
            sums = [s / 1e6 for s in sums]
            unit = "ms"
        elif "http_us" in k:
            sums = [s / 1e3 for s in sums]
            unit = "ms"
        else:
            unit = "n"
        med = statistics.median(sums) if sums else 0
        summary["spans"][k] = {"calls_per_run_median": statistics.median(ns),
                               "median_total": med, "unit": unit}
        print(f"{k:<58}{statistics.median(ns):>10.1f}{med:>9.2f}{unit}")
    return summary


def main():
    args = sys.argv[1:]

    def opt(name, default=None):
        if name in args:
            i = args.index(name)
            v = args[i + 1]
            del args[i:i + 2]
            return v
        return default

    out = opt("--json")
    raw = opt("--raw")
    mode = opt("--mode", "cold")
    fixture = opt("--fixture", "small")
    if "--warm" in args:
        args.remove("--warm")
        mode = "warm-new"
    if mode not in MODES:
        sys.exit(f"unknown mode {mode}")
    binary = os.path.abspath(args[0])
    runs = int(args[1]) if len(args) > 1 else 15
    fixture_root = opt("--fixture-root")
    probe = opt("--probe")
    if fixture_root:
        fixture = "scale"
        root = fixture_root
    else:
        root = tempfile.mkdtemp(prefix="crush-startup-")
    for d in ("home", "config", "data", "cache", "state", "work"):
        os.makedirs(f"{root}/{d}", exist_ok=True)
    sock = f"{root}/s.sock"
    trace = f"{root}/trace.txt"
    alltrace = f"{root}/all.txt"
    results = []
    first = None
    servers = set()
    srv = None
    try:
        proj, info = setup_fixture(binary, root, fixture)
        if mode == "reuse":
            srv = Server(binary, root, sock, trace)
        elif mode == "reuse-teardown":
            srv = Server(binary, root, sock, trace, grace="0s")
        for i in range(runs + 1):
            run_proj = proj
            if mode in ("cold", "warm-new"):
                run_proj = tempfile.mkdtemp(dir=f"{root}/work", prefix="proj-")
            # A long-lived server shares the per-run trace path, which
            # run_once truncates, so each run sees only its own records.
            r = run_once(binary, root, sock, trace, run_proj)
            _, _, pids = read_trace(trace)
            if os.path.exists(trace):
                with open(trace) as src, open(alltrace, "a") as dst:
                    dst.write(src.read())
            if mode == "cold":
                kill(pids)
                time.sleep(0.05)
            else:
                servers |= pids
            if mode == "reuse-teardown":
                time.sleep(0.5)
            if run_proj != proj:
                shutil.rmtree(run_proj, ignore_errors=True)
            if r is None:
                print(f"run {i}: timeout", file=sys.stderr)
                continue
            r["run"] = i
            if i == 0:
                first = r
                continue
            results.append(r)
        if probe and srv is not None:
            info["probe"] = probe_server(sock, srv.proc.pid)
    finally:
        if srv is not None:
            srv.stop()
        _, _, pids = read_trace(alltrace)
        kill(pids | servers)
        if fixture_root:
            for f in (sock, trace, alltrace):
                if os.path.exists(f):
                    os.unlink(f)
        else:
            shutil.rmtree(root, ignore_errors=True)
    if not results:
        sys.exit("no successful runs")
    print(f"fixture: {info}")
    summary = summarize(results, mode)
    summary["mode"] = mode
    summary["fixture"] = info
    if out:
        with open(out, "w") as f:
            json.dump(summary, f, indent=1)
    if raw:
        with open(raw, "w") as f:
            json.dump({"mode": mode, "fixture": info, "warmup": first,
                       "runs": results}, f, indent=1)


if __name__ == "__main__":
    main()
