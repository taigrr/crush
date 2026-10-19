package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/headless"
	"github.com/taigrr/crush/internal/mcpserver"
	"github.com/taigrr/crush/internal/proto"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Serve Crush to other agents over MCP (stdio)",
	Long: `Run an MCP server on stdin/stdout that lets another coding agent (Claude
Code, clawdbot, Cursor, ...) drive Crush: start runs, wait for them, answer
their permission prompts and questions, and review their changes.

Tools: crush_start, crush_wait, crush_status, crush_respond, crush_cancel,
crush_list, crush_diff.

Runs default to --permission-mode delegate: tool permissions wait for the
calling agent to decide with crush_respond. The flag is also the most
permissive mode a caller may request.

'crush skill print' prints a skill that teaches an agent this workflow.`,
	Example: `
# Claude Code
claude mcp add crush -- crush mcp

# Let callers choose yolo runs that approve their own tool calls
claude mcp add crush -- crush mcp --permission-mode yolo`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		modeFlag, _ := cmd.Flags().GetString("permission-mode")
		if modeFlag == "" {
			return fmt.Errorf("--permission-mode must be delegate, deny, or yolo")
		}
		mode, err := headless.ParsePermissionMode(modeFlag)
		if err != nil {
			return err
		}
		allowForeign, _ := cmd.Flags().GetBool("allow-foreign-sessions")
		maxWait, _ := cmd.Flags().GetDuration("max-wait")
		cwd, err := ResolveCwd(cmd)
		if err != nil {
			return err
		}

		ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		cmd.SetContext(ctx)

		conn := newMCPConnector(cmd)
		defer conn.Close()

		srv := mcpserver.New(conn, mcpserver.Options{
			DefaultCwd:           cwd,
			DefaultMode:          mode,
			MaxWait:              maxWait,
			AllowForeignSessions: allowForeign,
		})
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	},
}

func init() {
	mcpCmd.Flags().String("permission-mode", string(headless.ModeDelegate), "Default and most permissive permission mode callers may use: delegate, deny, or yolo")
	mcpCmd.Flags().Bool("allow-foreign-sessions", false, "Let callers answer, cancel, and send follow-ups to sessions this server did not start, such as TUI sessions")
	mcpCmd.Flags().Duration("max-wait", 0, "Longest a single crush_wait may block (default 9m); keep it below your client's tool timeout")
	rootCmd.AddCommand(mcpCmd)
}

// mcpConnector connects the MCP server to the Crush server, one
// workspace per project directory. Each workspace keeps an event stream
// open for the lifetime of the MCP server so it stays attached (and its
// LSPs warm) between tool calls.
type mcpConnector struct {
	cmd     *cobra.Command
	connect func(dir string) (*mcpEntry, error)

	mu      sync.Mutex
	closed  bool
	entries map[string]*mcpEntry
	// pending dedupes concurrent first connects to the same project
	// without holding mu (connecting can take seconds), so a slow
	// project never blocks calls for other projects.
	pending map[string]*mcpConnect
}

type mcpConnect struct {
	done  chan struct{}
	entry *mcpEntry
	err   error
}

type mcpEntry struct {
	client  mcpserver.Client
	ws      *proto.Workspace
	cancel  context.CancelFunc
	cleanup func()
	dead    chan struct{}
}

func newMCPConnector(cmd *cobra.Command) *mcpConnector {
	m := &mcpConnector{
		cmd:     cmd,
		entries: make(map[string]*mcpEntry),
		pending: make(map[string]*mcpConnect),
	}
	m.connect = m.dial
	return m
}

// Connect implements [mcpserver.Connector].
func (m *mcpConnector) Connect(ctx context.Context, dir string) (mcpserver.Client, *proto.Workspace, error) {
	key := canonicalDir(dir)
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, nil, errors.New("crush mcp is shutting down")
		}
		if e, ok := m.entries[key]; ok {
			select {
			case <-e.dead:
				delete(m.entries, key)
				m.mu.Unlock()
				e.close()
				continue
			default:
				m.mu.Unlock()
				return e.client, e.ws, nil
			}
		}
		if p, ok := m.pending[key]; ok {
			m.mu.Unlock()
			select {
			case <-p.done:
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			}
			if p.err != nil {
				return nil, nil, p.err
			}
			continue
		}
		p := &mcpConnect{done: make(chan struct{})}
		m.pending[key] = p
		dial := m.connect
		m.mu.Unlock()

		// The dial runs on the server's lifetime, not the caller's ctx:
		// other callers may be waiting on it.
		p.entry, p.err = dial(key)

		m.mu.Lock()
		delete(m.pending, key)
		if p.err == nil && m.closed {
			// Close ran while we were dialing; nothing will release
			// this connection later.
			p.entry.close()
			p.entry, p.err = nil, errors.New("crush mcp is shutting down")
		}
		if p.err == nil {
			m.entries[key] = p.entry
		}
		m.mu.Unlock()
		close(p.done)
		if p.err != nil {
			return nil, nil, p.err
		}
		return p.entry.client, p.entry.ws, nil
	}
}

// dial connects to the server for dir and readies its agent.
func (m *mcpConnector) dial(dir string) (*mcpEntry, error) {
	ctx := m.cmd.Context()
	c, ws, cleanup, err := connectToServerAt(m.cmd, dir)
	if err != nil {
		return nil, err
	}
	if !ws.Config.IsConfigured() {
		cleanup()
		return nil, fmt.Errorf("no providers configured for %s - run 'crush' once to set one up", dir)
	}
	if err := c.InitiateAgentProcessing(ctx, ws.ID); err != nil {
		slog.Warn("Failed to initiate agent processing", "error", err)
	}
	if err := waitForAgent(ctx, c, ws.ID); err != nil {
		cleanup()
		return nil, fmt.Errorf("agent not ready: %w", err)
	}
	if err := c.UpdateAgent(ctx, ws.ID); err != nil {
		slog.Warn("Failed to update agent", "error", err)
	}

	streamCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	events, err := c.SubscribeEvents(streamCtx, ws.ID)
	if err != nil {
		cancel()
		cleanup()
		return nil, err
	}
	return newMCPEntry(c, ws, events, cancel, cleanup), nil
}

// newMCPEntry drains events in the background; the entry is marked dead
// when the stream ends so the next Connect reconnects.
func newMCPEntry(c mcpserver.Client, ws *proto.Workspace, events <-chan any, cancel context.CancelFunc, cleanup func()) *mcpEntry {
	e := &mcpEntry{client: c, ws: ws, cancel: cancel, cleanup: cleanup, dead: make(chan struct{})}
	go func() {
		defer close(e.dead)
		for range events {
		}
	}()
	return e
}

// Close releases every workspace.
func (m *mcpConnector) Close() {
	m.mu.Lock()
	m.closed = true
	entries := m.entries
	m.entries = make(map[string]*mcpEntry)
	m.mu.Unlock()
	for _, e := range entries {
		e.close()
	}
}

func (e *mcpEntry) close() {
	e.cancel()
	e.cleanup()
}

// canonicalDir resolves symlinks so two spellings of one project share a
// connection.
func canonicalDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return filepath.Clean(dir)
}
