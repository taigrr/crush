#!/usr/bin/env bash
# End-to-end cluster harness: two containers pair, a session with
# uncommitted project files moves from node-a to node-b, replication
# carries edits back, and the session moves home again.
#
#   test/cluster/run.sh          # build, run, tear down
#   KEEP=1 test/cluster/run.sh   # leave containers up for poking around
set -euo pipefail

cd "$(dirname "$0")"
COMPOSE=(docker compose -f compose.yaml)
PASS=0

cleanup() {
	status=$?
	if [[ $status -ne 0 ]]; then
		echo "--- node-a log ---"
		"${COMPOSE[@]}" logs --no-color --tail=80 node-a || true
		echo "--- node-b log ---"
		"${COMPOSE[@]}" logs --no-color --tail=80 node-b || true
	fi
	if [[ -z "${KEEP:-}" ]]; then
		"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true
	fi
	exit $status
}
trap cleanup EXIT

a() { "${COMPOSE[@]}" exec -T node-a "$@"; }
b() { "${COMPOSE[@]}" exec -T node-b "$@"; }
ad() { "${COMPOSE[@]}" exec -T -w /work/demo node-a "$@"; }
bd() { "${COMPOSE[@]}" exec -T -w /work/demo node-b "$@"; }

step() { echo; echo "==> $*"; }
ok() { PASS=$((PASS + 1)); echo "    ok: $*"; }
fail() { echo "    FAIL: $*" >&2; exit 1; }

# eventually <timeout-seconds> <description> <command...>
eventually() {
	local timeout=$1 desc=$2
	shift 2
	local deadline=$((SECONDS + timeout))
	until "$@" >/dev/null 2>&1; do
		if ((SECONDS >= deadline)); then
			fail "timed out: $desc"
		fi
		sleep 1
	done
	ok "$desc"
}

step "Build and start node-a and node-b"
"${COMPOSE[@]}" up --build -d --wait 2>&1 | tail -3
eventually 30 "node-a server up" a crush cluster status
eventually 30 "node-b server up" b crush cluster status

step "Pair the nodes"
token=$(a crush cluster init --advertise node-a:7731 | grep -o 'crushc1_[A-Za-z0-9_-]*' | head -1)
[[ -n "$token" ]] || fail "no join token"
ok "node-a created a cluster"
"${COMPOSE[@]}" exec -T -d node-b crush cluster join node-a "$token" --advertise node-b:7731
join_pending() { a crush cluster status | grep -q 'Pending join requests'; }
eventually 20 "join request visible on node-a" join_pending
a crush cluster approve node-b >/dev/null
b_member() { b crush cluster status | grep -q 'node-a'; }
eventually 30 "node-b is a member" b_member
both_online() { [[ "$(a crush cluster top | grep -c ' online ')" == 2 ]]; }
eventually 30 "both nodes online in the dashboard" both_online
creds_synced() { b grep -q sk-harness-synced-from-node-a /root/.local/share/crush/crush.json; }
eventually 45 "freshly installed node-b received node-a's credentials" creds_synced

step "Seed a session on node-a"
ad crush session import /seed/claude.jsonl >/dev/null
sid=$(ad crush session list --json | jq -r '.[0].id')
[[ -n "$sid" && "$sid" != null ]] || fail "no session on node-a"
ok "imported session $sid"

step "Move the session to node-b"
ad crush session move "$sid" node-b | sed 's/^/    /'
bd test -f main.go || fail "main.go missing on node-b"
bd grep -q 'uncommitted work' main.go || fail "uncommitted edit did not travel"
bd grep -q 'from-node-a' .env || fail "untracked .env did not travel"
bd test -d .git || fail ".git did not travel"
bd test ! -e node_modules || fail "node_modules should not travel"
[[ "$(bd git rev-parse HEAD)" == "$(ad git rev-parse HEAD)" ]] || fail "HEAD differs"
ok "project files arrived intact (uncommitted, untracked, .git; no node_modules)"
bd crush session list --json | jq -e --arg id "$sid" 'any(.[]; .id == $id)' >/dev/null || fail "session missing on node-b"
ok "session history arrived on node-b"

step "Single owner: node-a may no longer write"
if out=$(ad crush run --session "$sid" "hello" 2>&1); then
	fail "node-a accepted a prompt for a session node-b owns"
fi
grep -qi "owned by another cluster node" <<<"$out" || fail "unexpected error: $out"
ok "node-a refuses to write the moved session"

step "Replicate an edit from the owner back to node-a"
bd crush session rename "$sid" "renamed on node-b" >/dev/null
renamed_on_a() { ad crush session list --json | jq -e --arg id "$sid" 'any(.[]; .id == $id and .title == "renamed on node-b")'; }
eventually 60 "rename replicated to node-a" renamed_on_a

step "Move it home again"
bd crush session move "$sid" node-a --on-conflict=keep | sed 's/^/    /'
ad crush session list --json | jq -e --arg id "$sid" 'any(.[]; .id == $id and .title == "renamed on node-b")' >/dev/null \
	|| fail "session did not come home with its latest state"
ok "session is back on node-a with node-b's edits"
if out=$(bd crush run --session "$sid" "hello" 2>&1); then
	fail "node-b still accepts prompts after handing the session back"
fi
grep -qi "owned by another cluster node" <<<"$out" || fail "unexpected error: $out"
ok "ownership returned: node-b is now the read-only side"

echo
echo "All $PASS checks passed."
