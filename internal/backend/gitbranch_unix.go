//go:build !windows

package backend

import (
	"os"
	"strings"
	"syscall"
)

func ownedByMe(path string) bool {
	if strings.Contains(path, "..") {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(st.Uid) == os.Geteuid()
}
