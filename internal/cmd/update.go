package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/server"
	"github.com/taigrr/crush/internal/update"
	"github.com/taigrr/crush/internal/version"
)

var (
	updateGraceful bool
	updateTimeout  time.Duration
)

func init() {
	updateCmd.Flags().BoolVar(&updateGraceful, "graceful", false, "Hand the running server off to this binary without killing in-flight work")
	updateCmd.Flags().DurationVar(&updateTimeout, "timeout", 0, "Give up waiting for the old server to drain after this long (0 = wait forever)")
	rootCmd.AddCommand(updateCmd)
}

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Check for a newer Crush release, or hand the running server off to this binary",
	Long: `Without flags, check GitHub for a newer Crush release and report it.

With --graceful, swap the background server for the one in this binary
without killing in-flight work. Replace the binary first (this command does
not download anything); then:

  1. If a server is running on a different version/build than this binary,
     ask it to drain: it stops accepting new prompts, finishes every
     in-flight turn (including ones waiting on a permission prompt — answer
     them to let the update proceed), keeps serving reads and event streams
     so open TUIs stay live, and exits on its own.
  2. Wait for it to exit, printing what it is still waiting on.
  3. Start the new server and print its version.

Queued prompts and swarm reply obligations are journaled in each workspace
database and picked up by the new server. Open TUIs reconnect to it and
re-attach their sessions by path.

If the running server already matches this binary, nothing happens.`,
	Example: `
# Report whether a newer release exists
crush update

# After replacing the crush binary, swap the server in without losing work
crush update --graceful

# Same, but fall back to a forced restart after five minutes
crush update --graceful --timeout 5m
`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if !updateGraceful {
			return reportAvailableUpdate(cmd.Context(), cmd.OutOrStdout())
		}
		hostURL, err := server.ParseHostURL(clientHost)
		if err != nil {
			return fmt.Errorf("invalid host URL: %v", err)
		}
		return gracefulUpdate(cmd, hostURL, updateTimeout, cmd.OutOrStdout())
	},
}

// reportAvailableUpdate is the flag-less `crush update`: the same check
// the TUI runs in the background, printed to out.
func reportAvailableUpdate(ctx context.Context, out io.Writer) error {
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := update.Check(checkCtx, version.Version, update.Default)
	if err != nil {
		return err
	}
	switch {
	case info.IsDevelopment():
		fmt.Fprintf(out, "Running a development build (%s); latest release is %s.\n", info.Current, info.Latest)
	case info.Available():
		fmt.Fprintf(out, "Update available: %s -> %s\n", info.Current, info.Latest)
		if info.URL != "" {
			fmt.Fprintln(out, info.URL)
		}
		fmt.Fprintln(out, "After installing it, run `crush update --graceful` to swap the server in without losing in-flight work.")
	default:
		fmt.Fprintf(out, "Crush %s is up to date.\n", info.Current)
	}
	return nil
}

// gracefulUpdate drains a stale running server, waits for it to exit,
// and spawns this binary as the replacement. A matching server is left
// alone. timeout <= 0 waits forever; otherwise the drain falls back to a
// forced stop when it elapses so the user is never stuck.
func gracefulUpdate(cmd *cobra.Command, hostURL *url.URL, timeout time.Duration, out io.Writer) error {
	ctx := cmd.Context()
	if hostURL.Scheme != "unix" && hostURL.Scheme != "npipe" {
		return fmt.Errorf("graceful update only manages socket-based servers (host is %s)", hostURL.Scheme)
	}
	if _, err := os.Stat(hostURL.Host); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(out, "No Crush server is running; starting one.")
		if err := spawnAndWaitReady(cmd, hostURL); err != nil {
			return fmt.Errorf("failed to start crush server: %v", err)
		}
		return printServerVersion(ctx, hostURL, out)
	}

	c, err := client.NewClient("", hostURL.Scheme, hostURL.Host)
	if err != nil {
		return err
	}
	vi, err := c.VersionInfo(ctx)
	if err != nil {
		if !isDeadSocketErr(err) {
			return fmt.Errorf("a server is listening on %s but did not answer /v1/version (%v); not touching it. Use `crush --reset` to force-restart it", hostURL.Host, err)
		}
		// Socket present but nobody answering: a stale file. Clean it up
		// and start fresh.
		fmt.Fprintf(out, "Server socket is stale (%v); starting a new server.\n", err)
		if err := spawnAndWaitReady(cmd, hostURL); err != nil {
			return fmt.Errorf("failed to start crush server: %v", err)
		}
		return printServerVersion(ctx, hostURL, out)
	}
	if sameBuild(vi) {
		fmt.Fprintf(out, "Server already running %s (build %s); nothing to do.\n", vi.Version, shortBuild(vi.BuildID))
		return nil
	}

	fmt.Fprintf(out, "Server is %s (build %s); this binary is %s (build %s).\n",
		vi.Version, shortBuild(vi.BuildID), version.Version, shortBuild(version.BuildID))
	res := drainAndWait(ctx, hostURL, c, vi, timeout, out)
	switch res {
	case drainResultExited:
		fmt.Fprintln(out, "Old server exited.")
	case drainResultUnsupported:
		fmt.Fprintln(out, "Old server predates graceful drain; stopping it (in-flight runs will be cancelled).")
		forceStopServer(ctx, hostURL, c, true, out)
	case drainResultTimeout:
		fmt.Fprintf(out, "Drain did not finish within %s; stopping the old server (in-flight runs will be cancelled).\n", timeout)
		forceStopServer(ctx, hostURL, c, true, out)
	case drainResultCanceled:
		return ctx.Err()
	}

	if err := spawnAndWaitReady(cmd, hostURL); err != nil {
		return fmt.Errorf("failed to start crush server: %v", err)
	}
	return printServerVersion(ctx, hostURL, out)
}

// drainResult classifies how drainAndWait ended.
type drainResult int

const (
	// drainResultExited: the old server finished draining and its socket
	// is gone.
	drainResultExited drainResult = iota
	// drainResultUnsupported: the old server does not implement drain
	// (older protocol); the caller must fall back to a forced stop.
	drainResultUnsupported
	// drainResultTimeout: the bounded wait elapsed with runs still active.
	drainResultTimeout
	// drainResultCanceled: the caller's context ended.
	drainResultCanceled
)

// drainProgressInterval is how often drainAndWait prints what it is
// still waiting on.
var drainProgressInterval = 5 * time.Second

// drainAndWait asks the server behind c to drain and blocks until its
// socket disappears, printing progress to out. out may be nil.
func drainAndWait(ctx context.Context, hostURL *url.URL, c *client.Client, vi *proto.VersionInfo, timeout time.Duration, out io.Writer) drainResult {
	if vi.ProtocolVersion < proto.MinDrainProtocolVersion {
		return drainResultUnsupported
	}
	if out == nil {
		out = io.Discard
	}
	h, err := c.DrainServer(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return drainResultCanceled
		}
		// A server on the same protocol that refuses to drain is not
		// something we can wait out.
		slog.Warn("Drain request failed; falling back to forced stop", "error", err)
		return drainResultUnsupported
	}
	fmt.Fprintf(out, "Draining: waiting on %d active run(s)...\n", h.ActiveRuns)

	var deadline <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		deadline = t.C
	}
	lastReport := time.Now()
	lastActive := h.ActiveRuns
	for {
		select {
		case <-ctx.Done():
			return drainResultCanceled
		case <-deadline:
			return drainResultTimeout
		case <-time.After(250 * time.Millisecond):
		}
		if _, err := os.Stat(hostURL.Host); errors.Is(err, fs.ErrNotExist) {
			return drainResultExited
		}
		probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		h, err := c.HealthInfo(probeCtx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return drainResultCanceled
			}
			if !isDeadSocketErr(err) {
				// Slow or odd answer from a server that is still there;
				// keep waiting rather than declaring it gone.
				continue
			}
			// The listener is gone; the socket file lags by at most a
			// moment. Only a refused connection justifies unlinking it.
			if !waitForSocketGone(ctx, hostURL, 2*time.Second) {
				_ = os.Remove(hostURL.Host)
			}
			return drainResultExited
		}
		if h.ActiveRuns != lastActive || time.Since(lastReport) >= drainProgressInterval {
			fmt.Fprintf(out, "Draining: waiting on %d active run(s)...\n", h.ActiveRuns)
			lastReport = time.Now()
			lastActive = h.ActiveRuns
		}
	}
}

// isDeadSocketErr reports whether err from a probe means nobody is
// listening on the socket (connection refused / file gone), as opposed
// to a live server that answered slowly or unexpectedly (timeout, bad
// body). Only the former justifies unlinking the socket; treating a
// slow or odd answer as "dead" would put two servers on one socket and
// one database.
func isDeadSocketErr(err error) bool {
	if err == nil {
		return false
	}
	// A slow answer is a live server, whatever wrapped it.
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return false
	}
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ENOENT)
}

// waitForSocketGone polls until the socket file disappears or d elapses.
func waitForSocketGone(ctx context.Context, hostURL *url.URL, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(hostURL.Host); errors.Is(err, fs.ErrNotExist) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
	_, err := os.Stat(hostURL.Host)
	return errors.Is(err, fs.ErrNotExist)
}

// forceStopServer is the "stop now" path used by --reset and by the
// drain fallbacks: send the shutdown control command and wait for the
// socket to go away. Only when the server was alive a moment ago (it
// answered /v1/version in this same invocation — `alive`) and ignores
// the request does it escalate to SIGTERM, then SIGKILL, against the
// pid the server recorded at startup — and only after verifying that
// pid still belongs to a crush process, since a pid file left by a
// crashed server may point at whatever reused the number. Finally the
// socket file is removed so a fresh server can bind. In-flight runs are
// cancelled; queued prompts are dropped (the old server clears its
// journal on a hard shutdown).
func forceStopServer(ctx context.Context, hostURL *url.URL, c *client.Client, alive bool, out io.Writer) {
	if out == nil {
		out = io.Discard
	}
	if c != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_ = c.ShutdownServer(shutdownCtx)
		cancel()
	}
	// The server's own orderly shutdown (CancelAll waits up to 5s, then
	// App.Shutdown) can legitimately take several seconds.
	if waitForSocketGone(ctx, hostURL, 8*time.Second) {
		removeServerPIDIfDead(hostURL)
		return
	}
	if !alive {
		// Nothing answered before we started; the socket and pid file
		// are leftovers. Never signal a pid we did not see serving.
		removeServerPIDIfDead(hostURL)
		removeStaleSocket(hostURL)
		return
	}
	pid, ok := readServerPID(hostURL)
	if !ok || !isCrushProcess(pid) {
		// Something is still listening (it answered a moment ago) and we
		// cannot positively identify it as a crush server, so we must
		// neither signal it nor unlink its socket; that would put two
		// servers on one database.
		fmt.Fprintf(out, "Server did not exit on request and its process could not be verified (pid file: %d); leaving it alone. Use `crush shutdown` or stop it manually.\n", pid)
		return
	}
	fmt.Fprintf(out, "Server did not exit on request; terminating pid %d.\n", pid)
	if killServerProcess(ctx, pid, killGracePeriod) {
		slog.Info("Terminated unresponsive server process", "pid", pid)
	}
	if !waitForSocketGone(ctx, hostURL, 2*time.Second) {
		// The process we just killed cannot unlink its socket any more;
		// only now is removing it safe.
		if !isCrushProcess(pid) {
			removeStaleSocket(hostURL)
		}
	}
	removeServerPIDIfDead(hostURL)
}

// killGracePeriod is how long killServerProcess gives a SIGTERM'd server
// to exit before SIGKILL. It must exceed the server's own orderly
// shutdown budget (CancelAll's 5s wait plus App.Shutdown).
const killGracePeriod = 12 * time.Second

// serverPIDFile is the per-host file the server writes its pid into at
// startup, so a client that needs to force-stop an unresponsive server
// knows which process to signal.
const serverPIDFile = "server.pid"

func serverPIDPath(hostURL *url.URL) (string, error) {
	dir, err := perHostServerDir(hostURL)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, serverPIDFile), nil
}

// writeServerPID records this process as the server for hostURL. The
// returned cleanup removes the file only if it still names this
// process: a replacement server may already have written its own pid
// while this one was unwinding.
func writeServerPID(hostURL *url.URL) (cleanup func()) {
	path, err := serverPIDPath(hostURL)
	if err != nil {
		return func() {}
	}
	own := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(path, []byte(own), 0o600); err != nil {
		slog.Warn("Failed to write server pid file", "path", path, "error", err)
		return func() {}
	}
	return func() {
		if raw, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(raw)) == own {
			_ = os.Remove(path)
		}
	}
}

// readServerPID returns the pid recorded by writeServerPID, if any.
func readServerPID(hostURL *url.URL) (int, bool) {
	path, err := serverPIDPath(hostURL)
	if err != nil {
		return 0, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return 0, false
	}
	return pid, true
}

// removeServerPIDIfDead deletes the pid file when the process it names
// is gone or is not a crush process, so a stale file never outlives the
// server it described.
func removeServerPIDIfDead(hostURL *url.URL) {
	pid, ok := readServerPID(hostURL)
	if !ok {
		return
	}
	if isCrushProcess(pid) {
		return
	}
	if path, err := serverPIDPath(hostURL); err == nil {
		_ = os.Remove(path)
	}
}

// printServerVersion reports the version of the server now listening.
func printServerVersion(ctx context.Context, hostURL *url.URL, out io.Writer) error {
	c, err := client.NewClient("", hostURL.Scheme, hostURL.Host)
	if err != nil {
		return err
	}
	vi, err := c.VersionInfo(ctx)
	if err != nil {
		return fmt.Errorf("new server started but did not report its version: %v", err)
	}
	fmt.Fprintf(out, "Server running %s (build %s).\n", vi.Version, shortBuild(vi.BuildID))
	return nil
}

// sameBuild reports whether the running server was built from the same
// source as this binary.
func sameBuild(vi *proto.VersionInfo) bool {
	return vi.Version == version.Version && vi.BuildID == version.BuildID
}

// shortBuild abbreviates a build id for display.
func shortBuild(id string) string {
	if id == "" {
		return "unknown"
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// staleServerWait is how long a TUI that finds a stale server waits for
// it to drain before falling back to a forced restart. Overridable via
// CRUSH_STALE_SERVER_WAIT (a Go duration; 0 waits forever).
func staleServerWait() time.Duration {
	const def = 30 * time.Second
	v := os.Getenv("CRUSH_STALE_SERVER_WAIT")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return def
	}
	return d
}
