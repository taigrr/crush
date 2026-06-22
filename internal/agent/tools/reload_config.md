Reloads Crush's configuration from disk, re-reading all config files
(global, project, and workspace `crush.json`) and refreshing providers,
models, and agents in the running server.

USE THIS WHEN:

- The user edited `crush.json` (or a `.local` variant) and wants the
  changes applied without restarting Crush.
- `crush_info` reports `dirty = true` under `[config]`, indicating the
  on-disk config has drifted from what the server loaded.

REQUIREMENTS:

- This tool is only available in Sysadmin Mode. If Sysadmin Mode is
  disabled the call fails with an explanatory message; ask the user to
  enable it from the command palette ("Enable Sysadmin Mode") and retry.

NOTES:

- The reload is atomic: if the new config fails to validate, the previous
  config is kept and an error is returned.
- Takes no parameters.
