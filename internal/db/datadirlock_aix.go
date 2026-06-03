//go:build aix

package db

import "errors"

// errLockContended is unused on AIX but declared so shared code in this
// package compiles. AIX is not a supported deployment target.
var errLockContended = errors.New("file lock is held by another process")

// tryFileLock is a no-op on AIX because golang.org/x/sys/unix does not
// expose flock there. The data-directory lock degrades to advisory-only
// on this platform.
func tryFileLock(path string) (func(), error) {
	return func() {}, nil
}
