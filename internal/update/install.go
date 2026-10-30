package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/taigrr/crush/internal/codesign"
)

// ModulePath is the Go module installed by [Install].
const ModulePath = "github.com/taigrr/crush"

var versionRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// ResolveVersion returns version normalized to a "vX.Y.Z" tag, looking
// up the latest GitHub release when version is empty or "latest".
func ResolveVersion(ctx context.Context, version string, client Client) (string, error) {
	if version == "" || version == "latest" {
		rel, err := client.Latest(ctx)
		if err != nil {
			return "", fmt.Errorf("failed to fetch latest release: %w", err)
		}
		version = rel.TagName
	}
	if !versionRe.MatchString(version) {
		return "", fmt.Errorf("invalid release version %q", version)
	}
	return "v" + strings.TrimPrefix(version, "v"), nil
}

// Install runs `go install` for the given release tag, replacing the
// crush binary in GOBIN (or GOPATH/bin). Output is streamed to stdout
// and stderr when they are non-nil.
func Install(ctx context.Context, tag string, stdout, stderr *os.File) error {
	if !versionRe.MatchString(tag) {
		return fmt.Errorf("invalid release version %q", tag)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("`go` not found in PATH; install Go 1.27+ to upgrade: %w", err)
	}
	dir, err := InstallDir()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, goBin, "install", ModulePath+"@"+tag)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOEXPERIMENT=greenteagc")
	if dir != "" {
		cmd.Env = append(cmd.Env, "GOBIN="+dir)
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install %s@%s: %w", ModulePath, tag, err)
	}
	signInstalled(ctx, goBin, dir, stderr)
	return nil
}

// signInstalled re-signs the freshly installed binary on darwin with a
// stable identifier. Failures are only warned about.
func signInstalled(ctx context.Context, goBin, dir string, stderr *os.File) {
	if codesign.Default.GOOS != "darwin" {
		return
	}
	if dir == "" {
		dir = goInstallDir(ctx, goBin)
	}
	if dir == "" {
		return
	}
	if _, err := codesign.Sign(ctx, filepath.Join(dir, "crush")); err != nil && stderr != nil {
		fmt.Fprintf(stderr, "Warning: failed to re-sign crush: %v\n", err)
	}
}

func goInstallDir(ctx context.Context, goBin string) string {
	out, err := exec.CommandContext(ctx, goBin, "env", "GOBIN", "GOPATH").Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		return strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		if gopath := strings.TrimSpace(strings.Split(lines[1], string(os.PathListSeparator))[0]); gopath != "" {
			return filepath.Join(gopath, "bin")
		}
	}
	return ""
}

// InstallDir is where [Install] puts the new binary: the directory of the
// running crush, so the copy the service and PATH actually launch is the
// one replaced. It returns "" (use go's default GOBIN) when the running
// binary is not an installed crush, such as a `go run` build.
func InstallDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return installDirFor(exe)
}

func installDirFor(exe string) (string, error) {
	name := strings.TrimSuffix(filepath.Base(exe), ".exe")
	dir := filepath.Dir(exe)
	if name != "crush" || strings.Contains(dir, "go-build") || strings.HasPrefix(dir, filepath.Clean(os.TempDir())) {
		return "", nil
	}
	if strings.Contains(dir, string(filepath.Separator)+"Cellar"+string(filepath.Separator)) {
		return "", fmt.Errorf("crush at %s is managed by Homebrew; upgrade it with brew instead", exe)
	}
	probe, err := os.CreateTemp(dir, ".crush-upgrade-*")
	if err != nil {
		return "", fmt.Errorf("cannot replace %s (directory not writable): %w", exe, err)
	}
	probe.Close()
	_ = os.Remove(probe.Name())
	return dir, nil
}
