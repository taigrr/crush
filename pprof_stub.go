//go:build !pprof

package main

import (
	"log/slog"
	"os"
)

// startProfiler is a no-op in builds without the pprof tag. Profiling
// support is opt-in to keep net/http/pprof out of release binaries.
func startProfiler() {
	if os.Getenv("CRUSH_PROFILE") != "" {
		slog.Warn("CRUSH_PROFILE is set but this binary was built without -tags pprof")
	}
}
