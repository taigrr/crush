//go:build aix

package cmd

// acquireSpawnLock is a no-op on AIX because golang.org/x/sys/unix does
// not expose flock there. AIX is not a supported deployment target;
// this stub exists only to keep the package compiling.
func acquireSpawnLock(path string) (func(), error) {
	return func() {}, nil
}
