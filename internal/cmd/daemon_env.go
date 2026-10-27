package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/taigrr/crush/internal/config"
)

// volatileEnv are variables tied to the installing terminal session that
// must not leak into a long-running daemon.
var volatileEnv = []string{
	"_", "PWD", "OLDPWD", "SHLVL", "TERM", "COLORTERM", "COLUMNS", "LINES",
	"DISPLAY", "WINDOWID", "SSH_TTY", "SSH_CONNECTION", "SSH_CLIENT",
	"SECURITYSESSIONID", "LaunchInstanceID",
}

var volatilePrefixes = []string{
	"TERM_", "TMUX", "ITERM", "GHOSTTY", "KITTY", "ALACRITTY", "WEZTERM", "VSCODE",
	"NVIM", "VIM", "ZELLIJ", "__CF", "XPC_", "CRUSH_DAEMON_",
}

// daemonEnvPath is where install-service snapshots the environment.
func daemonEnvPath() string {
	return filepath.Join(filepath.Dir(config.GlobalConfigData()), "daemon-env.json")
}

// snapshotEnv returns env without terminal-session variables.
func snapshotEnv(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" || slices.Contains(volatileEnv, k) {
			continue
		}
		if slices.ContainsFunc(volatilePrefixes, func(p string) bool { return strings.HasPrefix(k, p) }) {
			continue
		}
		out[k] = v
	}
	return out
}

// writeDaemonEnv saves the current shell environment (0600; it holds the
// same secrets your shell exports) for the daemon to load at startup, so
// a server started by launchd/systemd sees exactly what a terminal-started
// one would.
func writeDaemonEnv(path string) error {
	data, err := json.MarshalIndent(snapshotEnv(os.Environ()), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadDaemonEnv applies an environment snapshot to this process.
func loadDaemonEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var env map[string]string
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			return err
		}
	}
	return nil
}
