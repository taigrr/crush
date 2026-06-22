package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/server"
)

func init() {
	rootCmd.AddCommand(shutdownCmd)
}

var shutdownCmd = &cobra.Command{
	Use:   "shutdown",
	Short: "Shut down the background Crush server",
	Long: `Gracefully shut down the background Crush server.

The server keeps running in the background after all clients exit so that
subsequent sessions start quickly. Use this command to force a clean exit,
for example before upgrading Crush or freeing system resources.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		hostURL, err := server.ParseHostURL(clientHost)
		if err != nil {
			return fmt.Errorf("invalid host URL: %v", err)
		}

		// For socket-based hosts, a missing socket means no server is
		// running; avoid dialing (and avoid auto-spawning one).
		switch hostURL.Scheme {
		case "unix", "npipe":
			if _, statErr := os.Stat(hostURL.Host); errors.Is(statErr, fs.ErrNotExist) {
				fmt.Fprintln(os.Stdout, "No Crush server is running.")
				return nil
			}
		}

		c, err := client.NewClient("", hostURL.Scheme, hostURL.Host)
		if err != nil {
			return fmt.Errorf("failed to create client: %v", err)
		}

		if err := c.ShutdownServer(cmd.Context()); err != nil {
			return fmt.Errorf("failed to shut down server: %v", err)
		}

		// Best-effort wait for the socket to be released so the user
		// knows the server is actually gone before we return.
		if hostURL.Scheme == "unix" || hostURL.Scheme == "npipe" {
			for range 20 {
				if _, statErr := os.Stat(hostURL.Host); errors.Is(statErr, fs.ErrNotExist) {
					break
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
		}

		fmt.Fprintln(os.Stdout, "Crush server shut down.")
		return nil
	},
}
