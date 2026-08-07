Replace, insert, or delete an entire named symbol (function, method, type, class, or struct) using LSP document symbols to find its exact boundaries. Prefer this over `edit` for whole-symbol changes when the symbol begins at column 0 and occupies its own lines (e.g. Go/Rust/Python top-level and single-per-line declarations): it resolves the symbol's line range through the language server, so it never fails on whitespace or indentation mismatches inside the body. Requires user permission and shows a diff before applying.

Constraints — use the `edit` tool instead when any apply:
- The symbol does not start at column 0 (indented/nested members). Such symbols are rejected with an error.
- The symbol shares a physical line with another declaration (e.g. `type A int; type B int`, `export const Foo = ...` with trailing code). This tool operates on whole lines, so a co-located sibling or leading prefix would be lost.
- The language server returns flat symbol information without full declaration ranges (rejected with an error).

Parameters:
- `file_path`: the file containing the symbol.
- `symbol`: the symbol name to target. Must be unique within the file — if multiple symbols share the name, the tool returns the candidates and asks you to disambiguate (use `edit` for those).
- `operation`: `replace` (default, replace the entire symbol including signature and body), `insert_before` (insert content before the symbol), `insert_after` (insert content after the symbol), or `delete` (remove the symbol entirely).
- `new_content`: the replacement or inserted text. Required for `replace`, `insert_before`, and `insert_after`; ignored for `delete`.

On success, returns a summary plus LSP diagnostics for the file. If the symbol is not found, the available symbols are listed. If the file's language has no running LSP client, a clear error is returned.
