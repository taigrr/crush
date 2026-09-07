Edit a file by exact find-and-replace; can also create (empty old_string) or delete (empty new_string) content. For renames/moves use bash. For large rewrites use write.

You do not need to view the file first if you already know the text to replace. Matching is exact first, then tolerant of indentation and trailing-whitespace differences (new_string is re-indented to fit the file). If old_string is not found or matches more than once, the error includes a line-numbered view of the closest region(s) so the next attempt can use the exact text. Successful edits return the edited region with line numbers - no follow-up view is needed.

IMPORTANT: When you need to make multiple changes to the same file, use `multiedit` instead — it applies all edits in one call.
