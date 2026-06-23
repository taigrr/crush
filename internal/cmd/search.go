package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/embedding"
	"github.com/taigrr/crush/internal/historysearch"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/session"
)

var (
	searchJSON     bool
	searchSession  string
	searchScope    string
	searchLimit    int
	searchOffset   int
	searchNoVector bool
)

var searchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search conversation history",
	Long: `Search past conversation history with hybrid (substring + semantic) matching.

Results are ranked by Reciprocal Rank Fusion of the exact substring and
(when an embedder is configured) semantic vector signals. Use --json for
machine-readable output, or --no-vector to force pure substring search.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSearch,
}

func init() {
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Output as JSON")
	searchCmd.Flags().StringVar(&searchSession, "session", "", "Limit to a single session id")
	searchCmd.Flags().StringVar(&searchScope, "scope", "user", "Roles to search: 'user' or 'all'")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "Max results per page")
	searchCmd.Flags().IntVar(&searchOffset, "offset", 0, "Results to skip (pagination)")
	searchCmd.Flags().BoolVar(&searchNoVector, "no-vector", false, "Disable semantic search (substring only)")
	rootCmd.AddCommand(searchCmd)
}

func runSearch(cmd *cobra.Command, args []string) error {
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		return fmt.Errorf("query is required")
	}

	ctx := cmd.Context()
	dataDir, _ := cmd.Flags().GetString("data-dir")
	cfg, err := config.Init("", dataDir, false)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}
	if dataDir == "" {
		dataDir = cfg.Config().Options.DataDirectory
	}
	conn, err := db.Connect(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer conn.Close()

	queries := db.New(conn)
	sessions := session.NewService(queries, conn)
	messages := message.NewService(queries)
	emb := embedding.Build(queries, cfg.EmbeddingParams())

	var semantic *bool
	if searchNoVector {
		off := false
		semantic = &off
	}

	res, err := historysearch.Search(ctx, messages, sessions, emb, query, historysearch.Options{
		SessionID: searchSession,
		Scope:     historysearch.Scope(searchScope),
		Semantic:  semantic,
		Limit:     searchLimit,
		Offset:    max(searchOffset, 0),
	})
	if err != nil {
		return fmt.Errorf("search failed: %w", err)
	}

	if searchJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		return enc.Encode(searchJSONOutput(query, res))
	}

	return renderSearchTable(cmd, query, res)
}

// searchJSONResult is the JSON envelope for `crush search --json`.
type searchJSONResult struct {
	Query        string          `json:"query"`
	Total        int             `json:"total"`
	Offset       int             `json:"offset"`
	SemanticUsed bool            `json:"semantic_used"`
	Hits         []embedding.Hit `json:"hits"`
}

func searchJSONOutput(query string, res embedding.SearchResult) searchJSONResult {
	hits := res.Hits
	if hits == nil {
		hits = []embedding.Hit{}
	}
	return searchJSONResult{
		Query:        query,
		Total:        res.Total,
		Offset:       res.Offset,
		SemanticUsed: res.SemanticUsed,
		Hits:         hits,
	}
}

func renderSearchTable(cmd *cobra.Command, query string, res embedding.SearchResult) error {
	if res.Total == 0 {
		cmd.Printf("No matches for %q\n", query)
		return nil
	}
	if len(res.Hits) == 0 {
		cmd.Printf("No matches for %q at offset %d (only %d total)\n", query, res.Offset, res.Total)
		return nil
	}

	mode := "substring"
	if res.SemanticUsed {
		mode = "hybrid"
	}

	headers := []string{"#", "SCORE", "MATCH", "SESSION", "TITLE", "MESSAGE", "ROLE", "WHEN", "SNIPPET"}
	rows := make([][]string, 0, len(res.Hits))
	for _, h := range res.Hits {
		title := h.SessionTitle
		if title == "" {
			title = "(untitled)"
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", h.Rank),
			fmt.Sprintf("%.4f", h.Score),
			string(h.Match),
			h.SessionID,
			title,
			h.SourceID,
			h.Role,
			h.CreatedAt.Local().Format("2006-01-02 15:04"),
			h.Snippet,
		})
	}

	if term.IsTerminal(os.Stdout.Fd()) {
		// Truncate the snippet/title columns to keep the table within the
		// terminal width; the table library wraps otherwise.
		width := 120
		if tw, _, err := term.GetSize(os.Stdout.Fd()); err == nil && tw > 0 {
			width = tw
		}
		titleCap := clampInt(width/5, 12, 40)
		snippetCap := clampInt(width/3, 20, 80)
		for i := range rows {
			rows[i][4] = ansi.Truncate(rows[i][4], titleCap, "…")
			rows[i][8] = ansi.Truncate(strings.ReplaceAll(rows[i][8], "\n", " "), snippetCap, "…")
		}
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			StyleFunc(func(row, col int) lipgloss.Style {
				return lipgloss.NewStyle().Padding(0, 1)
			}).
			Headers(headers...).
			Rows(rows...)
		lipgloss.Println(t)
		cmd.Printf("Matches %d-%d of %d for %q (%s)\n",
			res.Offset+1, res.Offset+len(res.Hits), res.Total, query, mode)
		if res.Offset+len(res.Hits) < res.Total {
			cmd.Printf("Pass --offset %d for the next page.\n", res.Offset+len(res.Hits))
		}
		return nil
	}

	// Not a TTY: tab-separated, all columns, no truncation.
	cmd.Println(strings.Join(headers, "\t"))
	for _, r := range rows {
		clean := make([]string, len(r))
		for i, c := range r {
			clean[i] = strings.ReplaceAll(c, "\n", " ")
		}
		cmd.Println(strings.Join(clean, "\t"))
	}
	return nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
