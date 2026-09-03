Get Crush's current runtime state: active model, provider, LSP/MCP status, skills, hooks, permissions, and disabled tools. No parameters needed.

<usage>
- Shows the configured model roles (orchestrator, large, small, and any
  named roles), the full provider/model catalog you may delegate to via
  the `model` parameter on agent/review/swarm, LSP/MCP server status,
  skills, hooks, permissions mode, disabled tools, and key options
- Use when diagnosing why something isn't working (missing diagnostics,
  provider errors, MCP disconnections)
- No parameters needed — always returns the full current state
</usage>

<tips>
- Check [lsp] and [mcp] sections for service health
- Check [providers] to see which providers are enabled and available
- Check [model] and [model_catalog] before delegating with a `model`
  param: role names in [model] are portable, provider/model refs in
  [model_catalog] are exact
- Check [skills] to see which skills are available and whether they have been
  loaded this session
- Check [hooks] to see which hook events are configured and whether the
  hook runner is active
- Pair with the crush-config skill to fix configuration issues
</tips>
