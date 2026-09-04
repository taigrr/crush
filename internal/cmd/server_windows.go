//go:build windows
// +build windows

package cmd

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func addSignals(sigs []os.Signal) []os.Signal {
	return sigs
}

// killServerProcess terminates a server that ignored the shutdown
// control command. Windows has no graceful signal, so this is a hard
// kill after waiting out the grace period for it to exit on its own.
// Returns true when the process was found and killed.
func killServerProcess(ctx context.Context, pid int, grace time.Duration) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !isCrushProcess(pid) {
			return true
		}
		select {
		case <-ctx.Done():
			return true
		case <-time.After(50 * time.Millisecond):
		}
	}
	return proc.Kill() == nil
}

// isCrushProcess reports whether pid is alive and is a crush server
// process. The image name comes from tasklist; the command line from
// PowerShell's Get-CimInstance (wmic is absent on Windows 11 24H2+),
// then wmic as a fallback. When neither can produce a command line the
// image name alone decides, so a missing tool degrades to the older
// tasklist-only check rather than to "never ours". Errs on the side of
// "not ours" when even tasklist cannot answer.
func isCrushProcess(pid int) bool {
	if pid <= 0 {
		return false
	}
	spid := strconv.Itoa(pid)
	out, err := exec.Command("tasklist", "/FI", "PID eq "+spid, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return false
	}
	line := strings.ToLower(strings.TrimSpace(string(out)))
	if !strings.HasPrefix(line, "\"crush") {
		return false
	}
	cmdline, ok := windowsCommandLine(spid)
	if !ok {
		return true
	}
	return isServerCmdline(splitWindowsCommandLine(cmdline))
}

// windowsCommandLine returns pid's command line via PowerShell/CIM, then
// wmic. ok is false when neither tool produced one.
func windowsCommandLine(spid string) (string, bool) {
	ps := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-CimInstance Win32_Process -Filter 'ProcessId="+spid+"').CommandLine")
	if out, err := ps.Output(); err == nil {
		if line := strings.TrimSpace(string(out)); line != "" {
			return line, true
		}
	}
	out, err := exec.Command("wmic", "process", "where", "ProcessId="+spid, "get", "CommandLine", "/value").Output()
	if err != nil {
		return "", false
	}
	for _, l := range strings.Split(string(out), "\n") {
		l = strings.TrimSpace(l)
		if v, found := strings.CutPrefix(l, "CommandLine="); found && v != "" {
			return v, true
		}
	}
	return "", false
}

// splitWindowsCommandLine splits a Windows command line into arguments,
// honouring double quotes around the (possibly space-containing)
// executable path so argv[1] is the real first argument.
func splitWindowsCommandLine(s string) []string {
	var args []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			args = append(args, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return args
}

// isServerCmdline reports whether argv[1] is exactly the `server`
// subcommand.
func isServerCmdline(args []string) bool {
	return len(args) >= 2 && args[1] == "server"
}
