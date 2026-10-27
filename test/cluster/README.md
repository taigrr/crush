# Cluster harness

Two containers, built from separate Dockerfiles, exercise the cluster end
to end over a real network:

- `Dockerfile.node-a` — the "laptop": a git project at `/work/demo` with a
  committed file, an uncommitted edit, an untracked `.env`, and a
  `node_modules` tree, plus a Claude Code transcript to import.
- `Dockerfile.node-b` — the "mac mini": an empty always-on daemon.

`run.sh` pairs them, imports a session on node-a, moves it to node-b
(checking files, history, and that `node_modules` stayed behind),
verifies node-a refuses to write the moved session, renames it on node-b
and waits for the rename to replicate back, then moves it home.

```bash
task test:cluster            # or ./test/cluster/run.sh
KEEP=1 ./test/cluster/run.sh # leave containers running afterwards
docker compose -f test/cluster/compose.yaml exec node-a crush cluster top
```

Both images carry a placeholder provider (`seed/crush.json`) pointing at
an unreachable address so commands that require a provider get far
enough to hit cluster rules. Builds are dev builds, so
`CRUSH_CLUSTER_ALLOW_DEV=1` is set.
