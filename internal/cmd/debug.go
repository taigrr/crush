package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/client"
)

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "Inspect a running Crush server, locally or on a cluster node",
	Long: `Inspect a running Crush server: goroutine dumps, server logs, and the
transfers it is sending or receiving. Pass --node to ask another cluster
member (by name or node ID) through the attached server.`,
}

var debugGoroutinesCmd = &cobra.Command{
	Use:   "goroutines",
	Short: "Dump every goroutine's stack (find what a stuck server is waiting on)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := debugClient(cmd)
		if err != nil {
			return err
		}
		out, err := c.DebugGoroutines(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprint(cmd.OutOrStdout(), out)
		return nil
	},
}

var debugLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show the tail of the server log",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := debugClient(cmd)
		if err != nil {
			return err
		}
		lines, _ := cmd.Flags().GetInt("lines")
		level, _ := cmd.Flags().GetString("level")
		grep, _ := cmd.Flags().GetString("grep")
		raw, _ := cmd.Flags().GetBool("json")
		out, err := c.DebugLogs(cmd.Context(), lines, level, grep)
		if err != nil {
			return err
		}
		if raw {
			fmt.Fprint(cmd.OutOrStdout(), out)
			return nil
		}
		for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
			if line != "" {
				fmt.Fprintln(cmd.OutOrStdout(), formatLogLine(line))
			}
		}
		return nil
	},
}

var debugTransfersCmd = &cobra.Command{
	Use:   "transfers",
	Short: "List moves being sent and preflights/imports being received",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := debugClient(cmd)
		if err != nil {
			return err
		}
		out, err := c.Transfers(cmd.Context())
		if err != nil {
			return err
		}
		in, err := c.IncomingTransfers(cmd.Context())
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "DIRECTION\tSESSION\tSTEP\tPROGRESS\tRUNNING FOR")
		for _, s := range out {
			fmt.Fprintf(tw, "out → %s\t%s\t%s\t%d/%d files\t%s\n", s.TargetName, s.SessionID, s.Stage, s.Files, s.TotalFiles, since(s.StartedAt))
		}
		for _, s := range in {
			fmt.Fprintf(tw, "in (%s)\t%s\t%s\t-\t%s\n", s.Title, s.SessionID, s.Stage, since(s.StartedAt))
		}
		if len(out)+len(in) == 0 {
			fmt.Fprintln(tw, "(none)")
		}
		return tw.Flush()
	},
}

func init() {
	debugCmd.PersistentFlags().String("node", "", "Cluster member name or node ID to inspect (default: the attached server)")
	debugLogsCmd.Flags().IntP("lines", "n", 200, "Number of lines")
	debugLogsCmd.Flags().String("level", "", "Minimum level: debug, info, warn, error")
	debugLogsCmd.Flags().String("grep", "", "Only lines containing this text (case-insensitive)")
	debugLogsCmd.Flags().Bool("json", false, "Print raw JSON log records")
	debugCmd.AddCommand(debugGoroutinesCmd, debugLogsCmd, debugTransfersCmd)
	rootCmd.AddCommand(debugCmd)
}

// debugClient targets the attached server, or the cluster member named by
// --node.
func debugClient(cmd *cobra.Command) (*client.Client, error) {
	c, err := localServerClient(cmd)
	if err != nil {
		return nil, err
	}
	node, _ := cmd.Flags().GetString("node")
	if node == "" {
		return c, nil
	}
	st, err := c.ClusterStatus(cmd.Context())
	if err != nil {
		return nil, err
	}
	if node == st.Self.Name || node == st.Self.NodeID {
		return c, nil
	}
	for _, m := range st.Members {
		if m.Name == node || m.NodeID == node || strings.HasPrefix(m.NodeID, node) {
			return c.ForNode(m.NodeID), nil
		}
	}
	return nil, fmt.Errorf("no cluster member named %q", node)
}

// formatLogLine renders a JSON log record as "time LEVEL msg key=value".
func formatLogLine(line string) string {
	var rec map[string]any
	if json.Unmarshal([]byte(line), &rec) != nil {
		return line
	}
	ts, _ := rec["time"].(string)
	if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
		ts = t.Local().Format("15:04:05")
	}
	level, _ := rec["level"].(string)
	msg, _ := rec["msg"].(string)
	var b strings.Builder
	fmt.Fprintf(&b, "%s %-5s %s", ts, level, msg)
	for k, v := range rec {
		switch k {
		case "time", "level", "msg", "source":
			continue
		}
		fmt.Fprintf(&b, " %s=%v", k, v)
	}
	return b.String()
}

func since(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.Since(time.UnixMilli(ms)).Round(time.Second).String()
}
