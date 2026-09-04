Launch TWO independent adversarial reviewers in parallel to review a code change. Each reviewer runs in its own isolated context, sees only the change (not your reasoning), is told to assume the code is wrong, and reports bugs, regressions, and correctness issues. Reviewers are read-only: they can read code, trace symbols with LSP, and research libraries, but cannot edit or run anything. Both reports are returned to you so you — who hold the original goal and full context — can decide what to fix.

IMPORTANT: you do NOT paste the diff here. You pass a shell `command` whose stdout IS the change; the harness runs it in the working directory and feeds the output to the reviewers. This keeps large diffs out of the conversation.

Choosing the command:
- Feature branch off a base: `git diff $(git merge-base HEAD main)` (covers all your commits plus staged/unstaged edits).
- Committing directly on the base branch: `git diff @{upstream}` or `git diff origin/main` (includes unpushed local commits).
- Only uncommitted edits: `git diff HEAD`.
- No git at all: write the relevant files to a temp file (e.g. `{ echo '// file: src/foo.ts'; cat src/foo.ts; } > /tmp/review.txt`) and pass `cat /tmp/review.txt`.

If the command produces no output, the call errors — that usually means you chose the wrong base; widen it and retry. Use this as part of a write -> review -> fix loop when finalizing a non-trivial code change (creating a PR, shipping, finalizing, or when review is explicitly requested). Optionally include the original goal and specific concerns to focus on. After receiving the findings, apply the valid fixes yourself, then repeat the review if the changes were substantial, until the reviewers surface no real defects.

Pass `model` to run both reviewers on a specific model (a configured role name, `provider/model`, or a bare model id). A reviewer from a different vendor than the one that wrote the change tends to catch what the writer's vendor is blind to. Omitted runs the configured `worker` role, else the large model.
