package cmd

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/config"
	crushlog "github.com/taigrr/crush/internal/log"
	"github.com/taigrr/crush/internal/server"
	"github.com/taigrr/crush/internal/version"
)

var serverHost string

func init() {
	serverCmd.Flags().StringVarP(&serverHost, "host", "H", server.DefaultHost(), "Server host (TCP or Unix socket)")
	rootCmd.AddCommand(serverCmd)
}

// writeStderrBanner stamps a one-line build fingerprint onto the
// server's stderr. The detached server's stderr is redirected to
// stderr.log (see startDetachedServer), which is also where the Go
// runtime writes unrecoverable panics and fatal errors. Those traces
// carry module paths (crush@vX.Y.Z) but not the running build's
// commit/build id or pid, and stderr.log is append-only across many
// restarts, so without this banner it is hard to tell which version a
// given crash belongs to. Every panic that follows in the file is
// attributable to the most recent banner above it.
func writeStderrBanner(w io.Writer) {
	fmt.Fprintf(w,
		"=== crush server start: version=%s commit=%s build_id=%s pid=%d go=%s os=%s/%s time=%s ===\n",
		version.Version,
		version.Commit,
		version.BuildID,
		os.Getpid(),
		runtime.Version(),
		runtime.GOOS,
		runtime.GOARCH,
		time.Now().Format(time.RFC3339),
	)
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the Crush server",
	RunE: func(cmd *cobra.Command, _ []string) error {
		dataDir, err := cmd.Flags().GetString("data-dir")
		if err != nil {
			return fmt.Errorf("failed to get data directory: %v", err)
		}
		debug, err := cmd.Flags().GetBool("debug")
		if err != nil {
			return fmt.Errorf("failed to get debug flag: %v", err)
		}

		cfg, err := config.Load(config.GlobalWorkspaceDir(), dataDir, debug)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %v", err)
		}

		hostURL, err := server.ParseHostURL(serverHost)
		if err != nil {
			return fmt.Errorf("invalid server host: %v", err)
		}

		logFile := filepath.Join(config.GlobalCacheDir(), "server-"+safeHostName(hostURL), "crush.log")

		if term.IsTerminal(os.Stderr.Fd()) {
			crushlog.Setup(logFile, debug, os.Stderr)
		} else {
			crushlog.Setup(logFile, debug)
			writeStderrBanner(os.Stderr)
		}

		srv := server.NewServer(cfg, hostURL.Scheme, hostURL.Host)
		srv.SetLogger(slog.Default())
		slog.Info("Starting Crush server...", "addr", serverHost)

		errch := make(chan error, 1)
		sigch := make(chan os.Signal, 1)
		sigs := []os.Signal{os.Interrupt}
		sigs = append(sigs, addSignals(sigs)...)
		signal.Notify(sigch, sigs...)

		go func() {
			errch <- srv.ListenAndServe()
		}()

		select {
		case <-sigch:
			slog.Info("Received interrupt signal...")
		case err = <-errch:
			if err != nil && !errors.Is(err, server.ErrServerClosed) {
				_ = srv.Close()
				slog.Error("Server error", "error", err)
				return fmt.Errorf("server error: %v", err)
			}
		}

		if errors.Is(err, server.ErrServerClosed) {
			return nil
		}

		slog.Info("Shutting down...")

		// Stop tears down workspaces (cancelling in-flight runs) and
		// then drains/force-closes HTTP, so a signal doesn't hang on
		// open SSE streams or leave runs executing.
		srv.Stop()

		return nil
	},
}
