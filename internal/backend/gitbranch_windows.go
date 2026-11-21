//go:build windows

package backend

// ownedByMe always reports false on Windows so branch lookups defer to git,
// which applies its own ownership rules there.
func ownedByMe(string) bool { return false }
