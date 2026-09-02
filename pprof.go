//go:build pprof

package main

import (
	"cmp"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
)

// startProfiler serves net/http/pprof on localhost when CRUSH_PROFILE is
// set. Defaults to :6060 for the client; set CRUSH_PROFILE_PORT to use a
// different port (the server subprocess uses 6061).
func startProfiler() {
	if os.Getenv("CRUSH_PROFILE") == "" {
		return
	}
	addr := "localhost:" + cmp.Or(os.Getenv("CRUSH_PROFILE_PORT"), "6060")
	go func() {
		slog.Info("Serving pprof", "addr", addr)
		if httpErr := http.ListenAndServe(addr, nil); httpErr != nil {
			slog.Error("Failed to pprof listen", "error", httpErr)
		}
	}()
}
