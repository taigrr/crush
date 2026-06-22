package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/server"
)

func init() {
	rootCmd.AddCommand(reloadCmd)
}

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload the Crush config from disk",
	Long: `Force the background Crush server to re-read this workspace's config
files from disk and refresh providers, models, and agents.

Useful after editing crush.json while a long-lived server is serving the
workspace, since the server keeps running in the background between sessions.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		hostURL, err := server.ParseHostURL(clientHost)
		if err != nil {
			return fmt.Errorf("invalid host URL: %v", err)
		}

		// A missing socket means no server is running; there is nothing
		// to reload (the next session loads config fresh). Avoid
		// auto-spawning a server just to reload.
		switch hostURL.Scheme {
		case "unix", "npipe":
			if _, statErr := os.Stat(hostURL.Host); errors.Is(statErr, fs.ErrNotExist) {
				fmt.Fprintln(os.Stdout, "No Crush server is running; config will be loaded fresh on next start.")
				return nil
			}
		}

		c, ws, cleanup, err := connectToServer(cmd)
		if err != nil {
			return err
		}
		defer cleanup()

		if err := c.ReloadConfig(cmd.Context(), ws.ID); err != nil {
			return fmt.Errorf("failed to reload config: %v", err)
		}

		fmt.Fprintln(os.Stdout, "Crush config reloaded.")
		return nil
	},
}
