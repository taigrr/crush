package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
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
		}

		// A graceful update spawns this server while its predecessor
		// may still be finishing its drain. Never bind (and never open a
		// workspace database, which would race the old server's
		// migrations and data-dir lock) until it is gone.
		if err := waitForPredecessor(cmd.Context(), hostURL); err != nil {
			return err
		}

		srv := server.NewServer(cfg, hostURL.Scheme, hostURL.Host)
		srv.SetLogger(slog.Default())
		slog.Info("Starting Crush server...", "addr", serverHost, "version", version.Version, "build_id", version.BuildID)
		defer writeServerPID(hostURL)()

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

// predecessorWaitTimeout bounds how long a starting server waits for a
// draining predecessor on the same socket. Overridable via
// CRUSH_SERVER_TAKEOVER_WAIT (a Go duration).
func predecessorWaitTimeout() time.Duration {
	const def = 10 * time.Minute
	v := os.Getenv("CRUSH_SERVER_TAKEOVER_WAIT")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// waitForPredecessor handles an existing socket at hostURL before this
// server binds it:
//
//   - nobody answers: a stale file from a crashed server; remove it.
//   - a live server that is draining: wait for it to exit (the drain
//     endpoint's contract is that it removes its socket on the way out),
//     logging progress, bounded by predecessorWaitTimeout.
//   - a live server that is not draining: refuse to start; two servers
//     must never share a socket or a workspace database.
func waitForPredecessor(ctx context.Context, hostURL *url.URL) error {
	if hostURL.Scheme != "unix" && hostURL.Scheme != "npipe" {
		return nil
	}
	if _, err := os.Stat(hostURL.Host); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	c, err := client.NewClient("", hostURL.Scheme, hostURL.Host)
	if err != nil {
		return err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	h, err := c.HealthInfo(probeCtx)
	cancel()
	if err != nil {
		if !isDeadSocketErr(err) {
			// Something is listening but not answering as a current
			// server would (an older build's empty health body, or a
			// server too loaded to answer in time). Never unlink a
			// live socket: two servers on one socket would fight over
			// every workspace database.
			return fmt.Errorf("a process is listening on %s but did not answer the health probe (%v); refusing to start a second server. Use `crush --reset` to force-restart it", hostURL.Host, err)
		}
		slog.Info("Removing stale server socket left by a previous process", "path", hostURL.Host, "error", err)
		removeStaleSocket(hostURL)
		return nil
	}
	if !h.Draining {
		return fmt.Errorf("another Crush server is already listening on %s; use `crush update --graceful` to replace it or `crush shutdown` to stop it", hostURL.Host)
	}
	slog.Info("Previous server is draining; waiting for it to exit before taking over",
		"active_runs", h.ActiveRuns, "timeout", predecessorWaitTimeout())
	deadline := time.Now().Add(predecessorWaitTimeout())
	lastLog := time.Now()
	for {
		if _, err := os.Stat(hostURL.Host); errors.Is(err, fs.ErrNotExist) {
			slog.Info("Previous server exited; taking over")
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("previous server on %s is still draining after %s; run `crush --reset` to force it", hostURL.Host, predecessorWaitTimeout())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		h, err := c.HealthInfo(probeCtx)
		cancel()
		if err != nil {
			if !isDeadSocketErr(err) {
				// Slow answer from a server still unwinding; keep waiting.
				continue
			}
			// Listener gone; the socket file lags by at most a moment.
			if !waitForSocketGone(ctx, hostURL, 2*time.Second) {
				removeStaleSocket(hostURL)
			}
			slog.Info("Previous server exited; taking over")
			return nil
		}
		if time.Since(lastLog) >= 5*time.Second {
			slog.Info("Still waiting for previous server to finish draining", "active_runs", h.ActiveRuns)
			lastLog = time.Now()
		}
	}
}
