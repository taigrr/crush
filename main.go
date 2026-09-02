// Package main is the entry point for the Crush CLI.
//
//	@title			Crush API
//	@version		1.0
//	@description	Crush is a terminal-based AI coding assistant. This API is served over a Unix socket (or Windows named pipe) and provides programmatic access to workspaces, sessions, agents, LSP, MCP, and more.
//	@contact.name	Charm
//	@contact.url	https://charm.sh
//	@license.name	MIT
//	@license.url	https://github.com/taigrr/crush/blob/main/LICENSE
//	@BasePath		/v1
package main

import (
	_ "github.com/joho/godotenv/autoload"
	"github.com/taigrr/crush/internal/cmd"
	_ "github.com/taigrr/crush/internal/dns"
)

func main() {
	startProfiler()
	cmd.Execute()
}
