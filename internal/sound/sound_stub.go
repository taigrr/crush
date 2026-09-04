//go:build !darwin && !windows && !js && !cgo

package sound

import "errors"

// errNoBackend is returned by Play in binaries built without an audio
// backend: CGO_ENABLED=0 on a platform where oto needs cgo (linux, the
// BSDs, android). Release builds are such binaries.
var errNoBackend = errors.New("sound: no audio backend in this build (needs cgo on this platform)")

// Play is a no-op without an audio backend. It returns an error so callers
// can log it; PlayAsync logs at debug level, so a silent binary never
// disrupts an agent run.
func Play(Sound, string) error {
	return errNoBackend
}
