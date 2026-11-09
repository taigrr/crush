package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/server"
	"github.com/taigrr/crush/internal/update"
	"github.com/taigrr/crush/internal/version"
)

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

var upgradeCmd = &cobra.Command{
	Use:   "upgrade [version]",
	Short: "Install the latest (or given) Crush release and restart the daemon",
	Long: `Install a published Crush release with "go install" and restart the
background server so it runs the new binary. Requires Go on PATH.

Use "crush cluster upgrade" to upgrade every machine in a cluster.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := ""
		if len(args) == 1 {
			target = args[0]
		}
		_, err := upgradeLocal(cmd, target)
		return err
	},
}

// upgradeLocal installs target (empty means latest) and restarts the
// local server. It returns the resolved release tag.
func upgradeLocal(cmd *cobra.Command, target string) (string, error) {
	out := cmd.OutOrStdout()
	tag, err := update.ResolveVersion(cmd.Context(), target, update.Default)
	if err != nil {
		return "", err
	}
	if tag == "v"+strings.TrimPrefix(version.Version, "v") {
		fmt.Fprintf(out, "Already on %s.\n", tag)
		return tag, nil
	}
	fmt.Fprintf(out, "Installing crush %s...\n", tag)
	if err := update.Install(cmd.Context(), tag, os.Stdout, os.Stderr); err != nil {
		return "", err
	}
	restarted, err := cluster.RestartService(cmd.Context())
	switch {
	case err != nil:
		return tag, fmt.Errorf("installed %s but failed to restart the service: %w", tag, err)
	case restarted:
		// The service manager recreates the socket asynchronously; wait
		// here so callers don't race it (and restart it a second time).
		hostURL, err := server.ParseHostURL(clientHost)
		if err != nil {
			return tag, err
		}
		if err := waitForServiceReady(cmd.Context(), hostURL); err != nil {
			return tag, fmt.Errorf("installed %s but %w", tag, err)
		}
		fmt.Fprintf(out, "Installed %s and restarted the background service.\n", tag)
	default:
		if c, err := clientForExisting(); err == nil {
			_ = c.ShutdownServer(cmd.Context())
		}
		fmt.Fprintf(out, "Installed %s. The server will restart on next launch.\n", tag)
	}
	return tag, nil
}
