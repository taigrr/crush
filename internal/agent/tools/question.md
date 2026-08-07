Ask the user a single structured question when — and only when — you are genuinely blocked by an ambiguous, high-stakes decision you cannot safely resolve on your own. This is a **last resort**, not a courtesy. Do NOT use it for style preferences, minor clarifications, or anything you can infer from context, memory files (AGENTS.md/CRUSH.md/CLAUDE.md), existing code patterns, or reasonable defaults. In every one of those cases, proceed with a clearly stated assumption instead of calling this tool.

Disallowed uses (do not call this tool for these — make a reasonable assumption and state it instead):

- Coding style, naming, formatting, or file-layout preferences.
- Which library/approach to use when the codebase already shows a pattern.
- Minor clarifications you could resolve by reading more code, searching, or checking memory files.
- Confirming something you are already confident about ("just to be safe").
- Multi-part checklists of small questions — pick the most reasonable interpretation for each and move on.
- Anything the user could reasonably expect you to just decide yourself.

Appropriate uses (rare):

- An irreversible or destructive action whose scope is genuinely ambiguous (e.g. "delete everything under `/data`" when it's unclear whether that means one directory or the entire volume).
- Two explicit user requirements that conflict and cannot both be satisfied.
- A decision with major, hard-to-reverse consequences (e.g. dropping a production database column, force-pushing over shared history) where the user's intent is not otherwise inferable.

If you are unsure whether a situation qualifies, it does not qualify — proceed with your best assumption and clearly state it in your response so the user can correct you if needed.

<usage>
- kind: "single_choice" (pick exactly one of `options`), "multiple_choice"
  (pick zero or more of `options`), "free_text" (open-ended answer), or
  "yes_no" (a yes/no decision).
- prompt: the question, phrased so a short answer resolves it. State the
  blocking decision plainly.
- options: required (at least two entries) for single_choice and
  multiple_choice; ignored otherwise.
- Ask only one question per call. If you have several blocking questions,
  ask the single most important one first — you can ask again after it is
  answered.
- The tool blocks until the user answers or declines. A declined answer is
  not an error: proceed using your best judgment and say what assumption
  you made.
- Unavailable in non-interactive contexts (e.g. `crush run`, YOLO/skip-
  permissions mode): the call fails immediately with a clear error instead
  of hanging, so you can fall back to stating an assumption.
</usage>
