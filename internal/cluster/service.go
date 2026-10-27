package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

// ServiceLabel identifies the background daemon on launchd and systemd.
const ServiceLabel = "land.charm.crush"

// ServiceSpec describes the daemon to install.
type ServiceSpec struct {
	// Executable is the absolute path to the crush binary.
	Executable string
	// Args are passed after the executable (e.g. server --persistent).
	Args []string
	// LogDir receives stdout/stderr logs.
	LogDir string
	// Env is extra environment for the daemon (KEY=VALUE).
	Env []string
	// Shell, when set, launches the daemon through this login shell
	// (`<shell> -l -i -c "exec ..."`) so the variables the user exports
	// in their dotfiles (API keys, AWS profiles, MCP tokens) are present,
	// exactly as when the server is started from a terminal.
	Shell string
}

var launchdTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Exe}}</string>
{{- range .Args}}
		<string>{{.}}</string>
{{- end}}
	</array>
{{- if .Env}}
	<key>EnvironmentVariables</key>
	<dict>
{{- range .Env}}
		<key>{{.K}}</key>
		<string>{{.V}}</string>
{{- end}}
	</dict>
{{- end}}
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>5</integer>
	<key>ProcessType</key>
	<string>Background</string>
	<key>StandardOutPath</key>
	<string>{{.LogDir}}/daemon.out.log</string>
	<key>StandardErrorPath</key>
	<string>{{.LogDir}}/daemon.err.log</string>
</dict>
</plist>
`))

var systemdTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=Crush server ({{.Label}})
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.ExecStart}}
Restart=always
RestartSec=5
{{- range .Env}}
Environment="{{.K}}={{.V}}"
{{- end}}
StandardOutput=append:{{.LogDir}}/daemon.out.log
StandardError=append:{{.LogDir}}/daemon.err.log

[Install]
WantedBy=default.target
`))

type kv struct{ K, V string }

func splitEnv(env []string) []kv {
	var out []kv
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok && k != "" {
			out = append(out, kv{k, v})
		}
	}
	return out
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	_ = xmlEscapeTo(&b, s)
	return b.String()
}

func xmlEscapeTo(b *bytes.Buffer, s string) error {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	_, err := b.WriteString(r.Replace(s))
	return err
}

// LaunchdPlist renders the launchd agent for spec.
func LaunchdPlist(spec ServiceSpec) (string, error) {
	esc := func(xs []string) []string {
		out := make([]string, len(xs))
		for i, x := range xs {
			out[i] = xmlEscape(x)
		}
		return out
	}
	env := splitEnv(spec.Env)
	for i := range env {
		env[i] = kv{xmlEscape(env[i].K), xmlEscape(env[i].V)}
	}
	exe, args := spec.command()
	var b bytes.Buffer
	err := launchdTmpl.Execute(&b, map[string]any{
		"Label":  ServiceLabel,
		"Exe":    xmlEscape(exe),
		"Args":   esc(args),
		"Env":    env,
		"LogDir": xmlEscape(spec.LogDir),
	})
	return b.String(), err
}

func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\$%") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", "$$", "%", "%%")
	return `"` + r.Replace(s) + `"`
}

// SystemdUnit renders the systemd user unit for spec.
func SystemdUnit(spec ServiceSpec) (string, error) {
	exe, args := spec.command()
	parts := []string{systemdQuote(exe)}
	for _, a := range args {
		parts = append(parts, systemdQuote(a))
	}
	var b bytes.Buffer
	err := systemdTmpl.Execute(&b, map[string]any{
		"Label":     ServiceLabel,
		"ExecStart": strings.Join(parts, " "),
		"Env":       splitEnv(spec.Env),
		"LogDir":    spec.LogDir,
	})
	return b.String(), err
}

// ServicePath returns where the service definition is installed.
func ServicePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", ServiceLabel+".plist"), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "crush.service"), nil
	default:
		return "", fmt.Errorf("service install is not supported on %s", runtime.GOOS)
	}
}

// InstallService writes the service definition and (re)loads it. It
// returns the path written.
func InstallService(ctx context.Context, spec ServiceSpec) (string, error) {
	path, err := ServicePath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(spec.LogDir, 0o700); err != nil {
		return "", err
	}
	var content string
	switch runtime.GOOS {
	case "darwin":
		content, err = LaunchdPlist(spec)
	default:
		content, err = SystemdUnit(spec)
	}
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		target := fmt.Sprintf("gui/%d", os.Getuid())
		_ = run(ctx, "launchctl", "bootout", target+"/"+ServiceLabel)
		if err := run(ctx, "launchctl", "bootstrap", target, path); err != nil {
			return path, err
		}
		return path, run(ctx, "launchctl", "kickstart", "-k", target+"/"+ServiceLabel)
	default:
		if err := run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return path, err
		}
		return path, run(ctx, "systemctl", "--user", "enable", "--now", "crush.service")
	}
}

// UninstallService stops and removes the service definition.
func UninstallService(ctx context.Context) error {
	path, err := ServicePath()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		_ = run(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), ServiceLabel))
	default:
		_ = run(ctx, "systemctl", "--user", "disable", "--now", "crush.service")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if runtime.GOOS == "linux" {
		_ = run(ctx, "systemctl", "--user", "daemon-reload")
	}
	return nil
}

// RestartService restarts an installed daemon. It reports false when
// no service is installed.
func RestartService(ctx context.Context) (bool, error) {
	path, err := ServicePath()
	if err != nil {
		return false, nil
	}
	if _, err := os.Stat(path); err != nil {
		return false, nil
	}
	switch runtime.GOOS {
	case "darwin":
		return true, run(ctx, "launchctl", "kickstart", "-k", fmt.Sprintf("gui/%d/%s", os.Getuid(), ServiceLabel))
	default:
		return true, run(ctx, "systemctl", "--user", "restart", "crush.service")
	}
}

func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ServiceInstalled reports whether a daemon definition is installed.
func ServiceInstalled() bool {
	path, err := ServicePath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// command returns the argv the service manager runs.
func (spec ServiceSpec) command() (string, []string) {
	if spec.Shell == "" {
		return spec.Executable, spec.Args
	}
	parts := []string{"exec", shellQuote(spec.Executable)}
	for _, a := range spec.Args {
		parts = append(parts, shellQuote(a))
	}
	return spec.Shell, []string{"-l", "-i", "-c", strings.Join(parts, " ")}
}
