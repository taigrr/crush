// Package swagger embeds the generated OpenAPI specification for the Crush
// API. The spec is produced by `task swag`; only the JSON and YAML outputs
// are generated so the runtime binary does not link swaggo/swag and its Go
// type-checker dependencies.
package swagger

import _ "embed"

// JSON is the generated OpenAPI 2.0 specification.
//
//go:embed swagger.json
var JSON []byte
