package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/proto"
	"github.com/taigrr/crush/internal/server"
	"github.com/taigrr/crush/internal/transfer"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Pair machines into a Crush cluster",
	Long: `Pair machines into a peer-to-peer Crush cluster.

On the first machine:

  crush cluster init            # prints a join token
  crush cluster install-service # keep the server running in the background

On every other machine:

  crush cluster join <first-machine-ip> <token>
  crush cluster install-service

Approve the request on any existing member, either from the TUI prompt or
with "crush cluster approve <name>". See docs/cluster.md for details.`,
}

var (
	clusterJoinTimeout time.Duration
	clusterAdvertise   []string
)

func init() {
	clusterJoinCmd.Flags().DurationVar(&clusterJoinTimeout, "timeout", 10*time.Minute, "How long to wait for approval")
	for _, c := range []*cobra.Command{clusterInitCmd, clusterJoinCmd} {
		c.Flags().StringSliceVar(&clusterAdvertise, "advertise", nil, "Addresses (host:port) peers should use to reach this node (default: auto-detect)")
	}
	clusterCmd.AddCommand(
		clusterInitCmd,
		clusterJoinCmd,
		clusterTokenCmd,
		clusterStatusCmd,
		clusterApproveCmd,
		clusterDenyCmd,
		clusterRotateCmd,
		clusterLeaveCmd,
		clusterNameCmd,
		clusterPingCmd,
		clusterUpgradeCmd,
		clusterInstallServiceCmd,
		clusterUninstallServiceCmd,
	)
	rootCmd.AddCommand(clusterCmd)
}

// localServerClient connects to (and if needed starts) the local server.
func localServerClient(cmd *cobra.Command) (*client.Client, error) {
	hostURL, err := server.ParseHostURL(clientHost)
	if err != nil {
		return nil, fmt.Errorf("invalid host URL: %v", err)
	}
	if err := ensureServer(cmd, hostURL); err != nil {
		return nil, err
	}
	return client.NewClient("", hostURL.Scheme, hostURL.Host)
}

func advertisedEndpoints() []cluster.Endpoint {
	var eps []cluster.Endpoint
	for _, a := range clusterAdvertise {
		if a = strings.TrimSpace(a); a != "" {
			eps = append(eps, cluster.Endpoint{Kind: cluster.EndpointDirect, Addr: a})
		}
	}
	return eps
}

func printBroadcastErrors(w io.Writer, errs []string) {
	for _, e := range errs {
		fmt.Fprintf(w, "warning: %s\n", e)
	}
}

func printToken(w io.Writer, token string) {
	fmt.Fprintf(w, "Join token (keep it secret):\n\n  %s\n\n", token)
	fmt.Fprintf(w, "On another machine run:\n\n  crush cluster join <this-machine-ip> %s\n", token)
}

var clusterInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a new cluster with this machine as the first member",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		token, err := c.ClusterInit(cmd.Context(), proto.ClusterInitRequest{Endpoints: advertisedEndpoints()})
		if err != nil {
			return err
		}
		printToken(cmd.OutOrStdout(), token)
		return nil
	},
}

var clusterJoinCmd = &cobra.Command{
	Use:   "join <address> <token>",
	Short: "Join an existing cluster through one of its members",
	Long: `Join an existing cluster. <address> is any member's IP or host,
optionally with :port (default port ` + fmt.Sprint(cluster.DefaultPort) + `). The command waits until a
member approves the request.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		name := hostnameOr("<name>")
		if st, err := c.ClusterStatus(cmd.Context()); err == nil && st.Self.Name != "" {
			name = st.Self.Name
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Requesting to join via %s. Approve it from the Crush TUI on any member, or run:\n\n  crush cluster approve %s\n\nWaiting for approval...\n",
			args[0], name)
		st, err := c.ClusterJoin(cmd.Context(), proto.ClusterJoinRequest{
			Addr:           args[0],
			Token:          args[1],
			Endpoints:      advertisedEndpoints(),
			TimeoutSeconds: int(clusterJoinTimeout.Seconds()),
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Joined cluster %s with %d members.\n", st.Self.ClusterID, len(st.Members))
		if !cluster.ServiceInstalled() {
			fmt.Fprintln(cmd.OutOrStdout(), "Tip: run `crush cluster install-service` to keep this node online.")
		}
		return nil
	},
}

func hostnameOr(def string) string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return def
}

var clusterTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print the current join token",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		token, err := c.ClusterToken(cmd.Context())
		if err != nil {
			return err
		}
		printToken(cmd.OutOrStdout(), token)
		return nil
	},
}

var clusterStatusCmd = &cobra.Command{
	Use:     "status",
	Aliases: []string{"ls", "members", "pending"},
	Short:   "Show cluster members and pending join requests",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		st, err := c.ClusterStatus(cmd.Context())
		if err != nil {
			return err
		}
		renderClusterStatus(cmd.OutOrStdout(), st)
		return nil
	},
}

func renderClusterStatus(w io.Writer, st cluster.Status) {
	fmt.Fprintf(w, "Node:     %s (%s)\n", st.Self.Name, st.Self.NodeID)
	fmt.Fprintf(w, "Version:  %s\n", st.Self.Version)
	if !st.InCluster {
		fmt.Fprintln(w, "Cluster:  none (run `crush cluster init` or `crush cluster join`)")
		return
	}
	fmt.Fprintf(w, "Cluster:  %s (key v%d)\n", st.Self.ClusterID, st.KeyVersion)
	if st.Listening != "" {
		fmt.Fprintf(w, "Listen:   %s\n", st.Listening)
	} else {
		fmt.Fprintln(w, "Listen:   not running")
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tNODE\tVERSION\tLAST SEEN\tENDPOINTS")
	for _, m := range st.Members {
		name := m.Name
		seen := humanSince(m.LastSeen)
		if m.NodeID == st.Self.NodeID {
			name += " (this node)"
			seen = "-"
		}
		var eps []string
		for _, e := range m.Endpoints {
			eps = append(eps, e.Addr)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", name, shortID(m.NodeID), m.Version, seen, strings.Join(eps, ", "))
	}
	_ = tw.Flush()
	if len(st.Pending) > 0 {
		fmt.Fprintln(w, "\nPending join requests:")
		tw = tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "REQUEST\tNAME\tNODE\tFROM\tREQUESTED")
		for _, p := range st.Pending {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.ID, p.Name, shortID(p.NodeID), p.RemoteAddr, humanSince(p.RequestedAt))
		}
		_ = tw.Flush()
		fmt.Fprintln(w, "\nApprove with `crush cluster approve <request|name>`.")
	}
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func humanSince(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

var clusterApproveCmd = &cobra.Command{
	Use:   "approve <request|name|node-id>",
	Short: "Approve a pending join request",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		m, err := c.ClusterApprove(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Approved %s (%s).\n", m.Name, shortID(m.NodeID))
		return nil
	},
}

var clusterDenyCmd = &cobra.Command{
	Use:   "deny <request|name|node-id>",
	Short: "Deny a pending join request",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		r, err := c.ClusterDeny(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Denied %s (%s).\n", r.Name, shortID(r.NodeID))
		return nil
	},
}

var clusterRotateCmd = &cobra.Command{
	Use:   "rotate-key",
	Short: "Replace the cluster key; old join tokens stop working",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		res, err := c.ClusterRotateKey(cmd.Context())
		if err != nil {
			return err
		}
		printBroadcastErrors(cmd.ErrOrStderr(), res.Errors)
		if len(res.Errors) > 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "Unreachable members keep working until the next rotation; rotate again once they are online.")
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Cluster key rotated.")
		printToken(cmd.OutOrStdout(), res.Token)
		return nil
	},
}

var clusterLeaveCmd = &cobra.Command{
	Use:   "leave",
	Short: "Remove this machine from its cluster",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		res, err := c.ClusterLeave(cmd.Context())
		if err != nil {
			return err
		}
		printBroadcastErrors(cmd.ErrOrStderr(), res.Errors)
		fmt.Fprintln(cmd.OutOrStdout(), "Left the cluster.")
		return nil
	},
}

var clusterNameCmd = &cobra.Command{
	Use:   "name <name>",
	Short: "Set this node's display name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		st, err := c.ClusterSetName(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "This node is now %q.\n", st.Self.Name)
		return nil
	},
}

var clusterPingCmd = &cobra.Command{
	Use:   "ping",
	Short: "Check reachability and versions of every member",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		res, err := c.ClusterPing(cmd.Context())
		if err != nil {
			return err
		}
		if len(res) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No other members.")
			return nil
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tRTT\tVERSION\tSTATUS")
		for _, r := range res {
			status := "ok"
			if r.Error != "" {
				status = r.Error
			}
			fmt.Fprintf(tw, "%s\t%dms\t%s\t%s\n", r.Name, r.RTTMillis, r.Version, status)
		}
		return tw.Flush()
	},
}

var clusterUpgradeCmd = &cobra.Command{
	Use:   "upgrade [version]",
	Short: "Upgrade this machine and every member to a release (default latest)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := ""
		if len(args) == 1 {
			target = args[0]
		}
		tag, err := upgradeLocal(cmd, target)
		if err != nil {
			return err
		}
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		res, err := c.ClusterUpgrade(cmd.Context(), tag)
		if err != nil {
			return err
		}
		printBroadcastErrors(cmd.ErrOrStderr(), res.Errors)
		fmt.Fprintf(cmd.OutOrStdout(), "Asked every member to upgrade to %s. Check with `crush cluster ping`.\n", tag)
		return nil
	},
}

var clusterInstallServiceCmd = &cobra.Command{
	Use:   "install-service",
	Short: "Run the Crush server as a background service (launchd/systemd)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		unreachableXDGForLaunchd(cmd.ErrOrStderr())
		args := []string{"server", "--persistent"}
		if serviceShell(cmd) == "" {
			envPath := daemonEnvPath()
			if err := writeDaemonEnv(envPath); err != nil {
				return fmt.Errorf("snapshot environment: %w", err)
			}
			args = append(args, "--env-file", envPath)
			fmt.Fprintf(cmd.OutOrStdout(), "Saved your shell environment to %s (re-run install-service after changing it).\n", envPath)
		}
		if clientHost != server.DefaultHost() {
			args = append(args, "--host", clientHost)
		}
		env := []string{"PATH=" + os.Getenv("PATH")}
		for _, k := range []string{"HOME", "GOPATH", "GOBIN", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
			if v := os.Getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
		// Stop any ad-hoc server so the service can bind the socket.
		if c, err := clientForExisting(); err == nil {
			_ = c.ShutdownServer(cmd.Context())
			time.Sleep(500 * time.Millisecond)
		}
		path, err := cluster.InstallService(cmd.Context(), cluster.ServiceSpec{
			Executable: exe,
			Args:       args,
			LogDir:     serviceLogDir(),
			Env:        env,
			Shell:      serviceShell(cmd),
		})
		if err != nil {
			return fmt.Errorf("install service (%s): %w", path, err)
		}
		hostURL, err := server.ParseHostURL(clientHost)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
		defer cancel()
		if err := waitForServerReady(ctx, hostURL); err != nil {
			return fmt.Errorf("service installed but not answering yet (see %s): %w", serviceLogDir(), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed and started %s.\n", path)
		return nil
	},
}

// clientForExisting returns a client for an already-running local
// server without spawning one.
func clientForExisting() (*client.Client, error) {
	hostURL, err := server.ParseHostURL(clientHost)
	if err != nil {
		return nil, err
	}
	if isLocalSocket(hostURL) {
		if _, err := os.Stat(hostURL.Host); err != nil {
			return nil, err
		}
	}
	c, err := client.NewClient("", hostURL.Scheme, hostURL.Host)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.Health(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

var clusterUninstallServiceCmd = &cobra.Command{
	Use:   "uninstall-service",
	Short: "Stop and remove the background service",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := cluster.UninstallService(cmd.Context()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Service removed.")
		return nil
	},
}

var clusterTopCmd = &cobra.Command{
	Use:     "top",
	Aliases: []string{"dashboard"},
	Short:   "Show every node's health, resources, and running jobs",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		rows, err := c.ClusterDashboard(cmd.Context())
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "NODE\tSTATUS\tRTT\tSEEN\tCPU\tMEM\tDISK FREE\tJOBS\tVERSION")
		for _, r := range rows {
			name := r.Member.Name
			if r.Self {
				name += " *"
			}
			status := "online"
			if !r.Online {
				status = "OFFLINE"
			}
			rtt := "-"
			if r.RTTMillis >= 0 && !r.Self {
				rtt = fmt.Sprintf("%dms", r.RTTMillis)
			}
			seen := "-"
			if !r.Self {
				seen = humanSince(r.LastSeen)
			}
			cpu, mem, disk, jobs, ver := "-", "-", "-", "-", r.Member.Version
			if s := r.Stats; s != nil {
				cpu = fmt.Sprintf("%.0f%% of %d", s.CPUPercent, s.CPUCores)
				mem = fmt.Sprintf("%.0f%%", s.MemPercent)
				disk = fmt.Sprintf("%.0fG", float64(s.DiskFree)/(1<<30))
				jobs = fmt.Sprint(len(s.Jobs))
				ver = s.Version
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, status, rtt, seen, cpu, mem, disk, jobs, ver)
		}
		return tw.Flush()
	},
}

func init() {
	clusterCmd.AddCommand(clusterTopCmd)
}

// resolveNodeRef finds a member by name, node ID, or ID prefix.
func resolveNodeRef(st cluster.Status, ref string) (cluster.Member, error) {
	var matches []cluster.Member
	for _, m := range st.Members {
		if m.NodeID == ref || strings.EqualFold(m.Name, ref) || (len(ref) >= 4 && strings.HasPrefix(m.NodeID, ref)) {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 0:
		return cluster.Member{}, fmt.Errorf("no cluster node matches %q (see `crush cluster status`)", ref)
	case 1:
		return matches[0], nil
	}
	return cluster.Member{}, fmt.Errorf("%q matches %d nodes; use the node ID", ref, len(matches))
}

var sessionMoveCmd = &cobra.Command{
	Use:   "move <session> <node>",
	Short: "Move a session and its project files to another cluster node",
	Long: `Move a session (by ID, hash prefix, or swarm address) to another machine
in the cluster. The conversation history and the project's working tree —
including uncommitted changes and untracked files — travel together; the
local copy stays on disk and the session is archived here as moved.

A running turn is paused first and resumed on the destination unless
--no-resume is given.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		conflict, _ := cmd.Flags().GetString("on-conflict")
		noResume, _ := cmd.Flags().GetBool("no-resume")
		gc, _ := cmd.Flags().GetBool("gc")
		return withHeadlessSession(cmd, args[0], func(ctx context.Context, c *client.Client, ws *proto.Workspace, sess *proto.Session) error {
			st, err := c.ClusterStatus(ctx)
			if err != nil {
				return err
			}
			target, err := resolveNodeRef(st, args[1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Moving %q to %s...\n", sess.Title, target.Name)
			// A local replica cannot hand the session off; its owner must.
			mover, wsID := c, ws.ID
			if owner := sess.OwnerNode; owner != "" && owner != st.Self.NodeID {
				mover = c.ForNode(owner)
				owsp, err := mover.CreateWorkspace(ctx, proto.Workspace{Path: ws.Path})
				if err != nil {
					return fmt.Errorf("reach owner: %w", err)
				}
				defer func() { _ = mover.DeleteWorkspace(context.Background(), owsp.ID) }()
				wsID = owsp.ID
			}
			res, err := mover.MoveSession(ctx, proto.MoveSessionRequest{
				WorkspaceID: wsID,
				SessionID:   sess.ID,
				TargetNode:  target.NodeID,
				OnConflict:  transfer.ConflictPolicy(conflict),
				Resume:      !noResume,
				GC:          gc,
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Moved to %s (%s).\n", res.TargetNodeName, res.Plan)
			if res.BackupPath != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "The previous contents there were moved to %s.\n", res.BackupPath)
			}
			if res.Resumed {
				fmt.Fprintln(cmd.OutOrStdout(), "The paused turn was resumed there.")
			}
			return nil
		})
	},
}

func init() {
	sessionMoveCmd.Flags().String("on-conflict", "", "When the destination has different files: backup (move them aside) or keep (move only the conversation)")
	sessionMoveCmd.Flags().Bool("no-resume", false, "Do not resume a paused turn on the destination")
	sessionMoveCmd.Flags().Bool("gc", false, "Garbage-collect Crush snapshots before sending")
	sessionCmd.AddCommand(sessionMoveCmd)
}

var clusterMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Move every running job on this machine to other cluster nodes",
	Long: `Plan and perform a migration of this machine's running jobs to the rest of
the cluster: each running turn is paused, its session and project files are
shipped, and the turn resumes on the destination. Sessions of one project
stay together on one node. By default nodes with the most spare CPU and
memory are preferred; use --strategy even to round-robin.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		targets, _ := cmd.Flags().GetStringSlice("to")
		strategy, _ := cmd.Flags().GetString("strategy")
		all, _ := cmd.Flags().GetBool("all")
		yes, _ := cmd.Flags().GetBool("yes")
		noResume, _ := cmd.Flags().GetBool("no-resume")
		conflict, _ := cmd.Flags().GetString("on-conflict")
		c, err := localServerClient(cmd)
		if err != nil {
			return err
		}
		ctx := cmd.Context()
		var ids []string
		if len(targets) > 0 {
			st, err := c.ClusterStatus(ctx)
			if err != nil {
				return err
			}
			for _, t := range targets {
				m, err := resolveNodeRef(st, t)
				if err != nil {
					return err
				}
				ids = append(ids, m.NodeID)
			}
		}
		plan, err := c.PlanMigration(ctx, proto.MigrateRequest{
			Targets: ids, Strategy: strategy, AllSessions: all, OnConflict: transfer.ConflictPolicy(conflict),
		})
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if len(plan.Moves) == 0 {
			fmt.Fprintln(out, "Nothing to migrate (no running jobs; use --all to include idle sessions).")
			return nil
		}
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "SESSION\tPROJECT\tSTATE\t→ NODE")
		for _, m := range plan.Moves {
			state := "idle"
			if m.Busy {
				state = "running"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Title, m.Root, state, m.TargetName)
		}
		_ = tw.Flush()
		if !yes {
			fmt.Fprint(out, "\nProceed? [y/N] ")
			var answer string
			_, _ = fmt.Fscanln(cmd.InOrStdin(), &answer)
			if !strings.EqualFold(strings.TrimSpace(answer), "y") {
				fmt.Fprintln(out, "Aborted.")
				return nil
			}
		}
		res, err := c.ExecuteMigration(ctx, plan, !noResume)
		if err != nil {
			return err
		}
		failed := 0
		for _, o := range res.Outcomes {
			if o.Error != "" {
				failed++
				fmt.Fprintf(cmd.ErrOrStderr(), "✗ %s → %s: %s\n", o.Title, o.TargetName, o.Error)
				continue
			}
			fmt.Fprintf(out, "✓ %s → %s\n", o.Title, o.TargetName)
		}
		fmt.Fprintf(out, "\nMigrated %d of %d sessions.\n", res.Moved, len(res.Outcomes))
		if failed > 0 {
			return fmt.Errorf("%d sessions did not move", failed)
		}
		return nil
	},
}

func init() {
	clusterMigrateCmd.Flags().StringSlice("to", nil, "Only migrate to these nodes (names or IDs)")
	clusterMigrateCmd.Flags().String("strategy", "capacity", "Placement: capacity (most free resources) or even (round-robin)")
	clusterMigrateCmd.Flags().Bool("all", false, "Include every idle session on this machine, not just running jobs")
	clusterMigrateCmd.Flags().BoolP("yes", "y", false, "Do not ask for confirmation")
	clusterMigrateCmd.Flags().Bool("no-resume", false, "Do not resume paused turns on their destinations")
	clusterMigrateCmd.Flags().String("on-conflict", "", "When a destination has different files: backup or keep")
	clusterCmd.AddCommand(clusterMigrateCmd)
}

// serviceShell returns the login shell to launch the daemon through when
// --login-shell is set. By default the daemon instead loads a snapshot of
// the installing shell's environment (see writeDaemonEnv), because many
// interactive dotfiles (tmux auto-attach, prompts) fail without a TTY.
func serviceShell(cmd *cobra.Command) string {
	if on, _ := cmd.Flags().GetBool("login-shell"); !on {
		return ""
	}
	if sh := os.Getenv("SHELL"); sh != "" && filepath.IsAbs(sh) {
		return sh
	}
	return ""
}

func init() {
	clusterInstallServiceCmd.Flags().Bool("login-shell", false, "Launch the server through your interactive login shell instead of an environment snapshot")
}

// serviceLogDir is where the daemon writes stdout/stderr. On macOS this
// is ~/Library/Logs/Crush: launchd refuses to start an agent whose log
// paths live on a volume it cannot reach at login (an external XDG cache,
// for example), failing with EX_CONFIG.
func serviceLogDir() string {
	if runtime.GOOS == "darwin" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Logs", "Crush")
		}
	}
	return filepath.Join(config.GlobalCacheDir(), "daemon")
}

// unreachableXDGForLaunchd unsets XDG_* directories on external volumes
// (/Volumes/...) before installing a macOS service. A launchd agent needs
// explicit privacy approval to touch removable or network volumes; without
// it, the first open() there blocks forever and the server never starts.
// Crush falls back to its defaults under the home directory instead.
func unreachableXDGForLaunchd(w io.Writer) {
	if runtime.GOOS != "darwin" {
		return
	}
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		if v := os.Getenv(k); strings.HasPrefix(v, "/Volumes/") {
			fmt.Fprintf(w, "warning: %s=%s is on an external volume launchd agents cannot use without privacy approval; the service will use the default location instead.\n", k, v)
			_ = os.Unsetenv(k)
		}
	}
}
