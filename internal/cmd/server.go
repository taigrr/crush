package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/config"
	crushlog "github.com/taigrr/crush/internal/log"
	"github.com/taigrr/crush/internal/server"
	"github.com/taigrr/crush/internal/update"
	"github.com/taigrr/crush/internal/version"
)

var (
	serverHost        string
	serverPersistent  bool
	serverClusterAddr string
	serverMeshAddr    string
)

func init() {
	serverCmd.Flags().StringVarP(&serverHost, "host", "H", server.DefaultHost(), "Server host (TCP or Unix socket)")
	serverCmd.Flags().BoolVar(&serverPersistent, "persistent", false, "Keep running after the last workspace closes (daemon mode)")
	serverCmd.Flags().StringVar(&serverClusterAddr, "cluster-addr", fmt.Sprintf(":%d", cluster.DefaultPort), "Address for the cluster listener (used once this node joins a cluster)")
	serverCmd.Flags().StringVar(&serverMeshAddr, "mesh-addr", fmt.Sprintf(":%d", cluster.DefaultRoutePort), "Address for the cluster NATS mesh routes")
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
		srv.Backend().SetPersistent(serverPersistent)
		if err := enableCluster(srv); err != nil {
			slog.Error("Failed to enable clustering", "error", err)
		}
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

// clusterDir is where this machine's node identity and cluster
// membership live.
func clusterDir() string {
	return filepath.Join(filepath.Dir(config.GlobalConfigData()), "cluster")
}

// enableCluster loads the node identity and starts the cluster listener
// when this node already belongs to a cluster. Upgrade requests from
// peers install the requested release and stop the server so the
// service manager restarts it on the new binary.
func enableCluster(srv *server.Server) error {
	node, err := cluster.NewNode(clusterDir(), version.Version)
	if err != nil {
		return err
	}
	node.OnUpgrade = func(target string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		tag, err := update.ResolveVersion(ctx, target, update.Default)
		if err != nil {
			return err
		}
		if tag == "v"+strings.TrimPrefix(version.Version, "v") {
			slog.Info("Cluster upgrade skipped; already on target", "version", tag)
			return nil
		}
		if err := update.Install(ctx, tag, nil, nil); err != nil {
			return err
		}
		slog.Info("Upgrade installed; restarting server", "version", tag)
		srv.Backend().Shutdown()
		return nil
	}
	return srv.EnableCluster(node, serverClusterAddr, serverMeshAddr)
}
