// Package codesign re-signs darwin binaries with a stable ad-hoc
// identifier so macOS keeps privacy grants (such as removable-volume
// access) across rebuilds instead of treating each build as "a.out".
package codesign

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// Identifier is the stable code signing identifier for crush binaries.
const Identifier = "land.charm.crush"

// Signer re-signs binaries. Its fields are injectable for tests.
type Signer struct {
	GOOS     string
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Default is a Signer for the running system.
var Default = Signer{
	GOOS:     runtime.GOOS,
	LookPath: exec.LookPath,
	Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	},
}

// Sign ad-hoc signs binary with [Identifier]. It reports whether the
// binary was signed. It is a no-op off darwin or when codesign is not
// installed. A non-nil error is meant to be shown as a warning.
func (s Signer) Sign(ctx context.Context, binary string) (bool, error) {
	if s.GOOS != "darwin" {
		return false, nil
	}
	bin, err := s.LookPath("codesign")
	if err != nil {
		return false, nil
	}
	out, err := s.Run(ctx, bin, "-s", "-", "-i", Identifier, "-f", binary)
	if err != nil {
		return false, fmt.Errorf("codesign %s: %w: %s", binary, err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// Sign signs binary with [Default].
func Sign(ctx context.Context, binary string) (bool, error) {
	return Default.Sign(ctx, binary)
}
