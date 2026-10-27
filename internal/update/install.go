package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
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
	cmd := exec.CommandContext(ctx, goBin, "install", ModulePath+"@"+tag)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOEXPERIMENT=greenteagc")
	if stdout != nil {
		cmd.Stdout = stdout
	}
	if stderr != nil {
		cmd.Stderr = stderr
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go install %s@%s: %w", ModulePath, tag, err)
	}
	return nil
}
