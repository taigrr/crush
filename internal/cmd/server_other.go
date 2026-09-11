//go:build !windows

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func addSignals(sigs []os.Signal) []os.Signal {
	return append(sigs, syscall.SIGTERM)
}

// killServerProcess escalates from SIGTERM to SIGKILL against a server
// that ignored the shutdown control command, giving it grace to exit
// cleanly in between. Returns true when a signal was delivered.
func killServerProcess(ctx context.Context, pid int, grace time.Duration) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return false
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return true
		}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(50 * time.Millisecond):
		}
	}
	_ = proc.Signal(syscall.SIGKILL)
	return true
}

// isCrushProcess reports whether pid is alive and is a crush *server*
// process. It is the guard against a stale pid file naming a number the
// kernel has since handed to an unrelated process — or to a crush TUI.
// The executable is read via /proc where available (Linux) and via `ps`
// elsewhere; the command line must contain the `server` subcommand. When
// nothing can answer, it errs on the side of "not ours".
//
// After a binary swap the running server's /proc exe link reads
// ".../crush (deleted)": that suffix is stripped before matching, and
// the full path is compared against this process's own executable as an
// alternative to the basename so a renamed dev binary still matches.
func isCrushProcess(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	exe, args := processExeAndArgs(pid)
	if exe == "" {
		return false
	}
	return isCrushExeName(exe) && isServerCmdline(args)
}

// processExeAndArgs returns the executable path and full argument list of
// pid, via /proc on Linux and `ps` elsewhere. Empty exe means unknown.
func processExeAndArgs(pid int) (exe string, args []string) {
	spid := strconv.Itoa(pid)
	if link, err := os.Readlink("/proc/" + spid + "/exe"); err == nil {
		exe = strings.TrimSuffix(link, " (deleted)")
		if raw, err := os.ReadFile("/proc/" + spid + "/cmdline"); err == nil {
			args = strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		}
		return exe, args
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", spid).Output()
	if err != nil {
		return "", nil
	}
	exe = strings.TrimSpace(string(out))
	if out, err := exec.Command("ps", "-o", "args=", "-p", spid).Output(); err == nil {
		args = strings.Fields(strings.TrimSpace(string(out)))
	}
	return exe, args
}

// isCrushExeName matches the executable this process is running (so a
// renamed or dev-built binary still counts) or anything named crush.
func isCrushExeName(path string) bool {
	base := filepath.Base(path)
	if base == "crush" || strings.HasPrefix(base, "crush.") || strings.HasPrefix(base, "crush-") {
		return true
	}
	if self, err := os.Executable(); err == nil {
		if path == self || filepath.Base(self) == base {
			return true
		}
		if resolved, err := filepath.EvalSymlinks(self); err == nil && resolved == path {
			return true
		}
	}
	return false
}

// isServerCmdline reports whether the argument list is a `crush server`
// invocation (as opposed to a TUI or `crush run`). The subcommand must
// be argv[1] exactly: `ps -o args=` is whitespace-split on macOS/BSD, so
// a prompt containing the word "server" would otherwise match. An
// unknown argument list is not trusted.
func isServerCmdline(args []string) bool {
	return len(args) >= 2 && args[1] == "server"
}
