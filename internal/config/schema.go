package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/invopop/jsonschema"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// desiredSchemaRef is the value we want the "$schema" field to point at
// in config files that live next to schema.json. A relative path keeps
// the reference portable across machines.
const desiredSchemaRef = "schema.json"

// writeGlobalSchema drops a fresh schema.json next to the global config
// file. This lets editors validate fork-specific fields (e.g. swarm)
// against the current binary's schema without depending on an upstream
// URL. Failures are logged and swallowed.
func writeGlobalSchema() {
	path := filepath.Join(filepath.Dir(GlobalConfig()), "schema.json")

	reflector := new(jsonschema.Reflector)
	bts, err := json.MarshalIndent(reflector.Reflect(&Config{}), "", "  ")
	if err != nil {
		slog.Warn("Failed to marshal config schema", "error", err)
		return
	}
	bts = append(bts, '\n')

	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(bts) {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		slog.Warn("Failed to create config dir for schema.json", "path", path, "error", err)
		return
	}
	if err := atomicWriteFile(path, bts, 0o644); err != nil {
		slog.Warn("Failed to write schema.json", "path", path, "error", err)
	}

	// Update $schema in sibling config files to point at the freshly
	// written local schema.
	for _, cfg := range []string{GlobalConfig(), GlobalConfigData()} {
		if filepath.Dir(cfg) == filepath.Dir(path) {
			updateSchemaRef(cfg)
		}
	}
}

// updateSchemaRef rewrites the "$schema" field in the given JSON config
// file to point at the local schema.json sibling, unless it already
// does. Missing files are ignored.
func updateSchemaRef(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if !json.Valid(data) {
		return
	}
	current := gjson.GetBytes(data, `$schema`).String()
	if current == desiredSchemaRef {
		return
	}
	updated, err := sjson.SetBytes(data, `$schema`, desiredSchemaRef)
	if err != nil {
		slog.Warn("Failed to update $schema reference", "path", path, "error", err)
		return
	}
	if err := atomicWriteFile(path, updated, 0o600); err != nil {
		slog.Warn("Failed to write updated $schema reference", "path", path, "error", err)
	}
}
