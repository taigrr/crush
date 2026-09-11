Edit a file by exact find-and-replace; can also create (empty old_string) or delete (empty new_string) content. For renames/moves use bash. For large rewrites use write.

You do not need to view the file first if you already know the text to replace. Matching is exact first, then tolerant of indentation and trailing-whitespace differences (new_string is re-indented to fit the file). If old_string is not found or matches more than once, the error includes a line-numbered view of the closest region(s) so the next attempt can use the exact text. Successful edits return the edited region with line numbers - no follow-up view is needed.

Set `regex: true` to treat old_string as a Go RE2 regex and new_string as a replacement template (`$1`, `${name}`; use `$$` for a literal `$`). Use `(?m)` for line anchors and `(?s)` to let `.` span lines (e.g. delete a whole function by matching its signature through the closing brace). Without replace_all the pattern must match exactly once. RE2 has no lookahead/lookbehind: capture the surrounding context and re-emit it instead.

Set `verify` to a shell command (e.g. `go vet ./...`, `tsc --noEmit`, `npm test`) to run after the edit succeeds; its output is appended to the result so you see the consequences in the same turn. Same permission rules as the bash tool.

IMPORTANT: When you need to make multiple changes to the same file, use `multiedit` instead — it applies all edits in one call.
