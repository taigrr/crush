package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/embedding"
)

var (
	embeddingsJSON       bool
	embeddingsDimensions int64
	embeddingsNoNormular bool
)

var embeddingsCmd = &cobra.Command{
	Use:     "embeddings",
	Aliases: []string{"embedding", "embed"},
	Short:   "Manage the global embedding model for hybrid history search",
	Long: `Manage the single, global text-embedding model used for vector and
hybrid history search. The embedder is set once in your global config
(~/.config/crush) and used across all projects; changing it invalidates
previously stored vectors.`,
}

var embeddingsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available embedding models",
	RunE:  runEmbeddingsList,
}

var embeddingsSetCmd = &cobra.Command{
	Use:   "set <provider> <model>",
	Short: "Set the global embedding model",
	Args:  cobra.ExactArgs(2),
	RunE:  runEmbeddingsSet,
}

var embeddingsStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active embedder and index status for this project",
	RunE:  runEmbeddingsStatus,
}

func init() {
	embeddingsListCmd.Flags().BoolVar(&embeddingsJSON, "json", false, "Output as JSON")
	embeddingsStatusCmd.Flags().BoolVar(&embeddingsJSON, "json", false, "Output as JSON")
	embeddingsSetCmd.Flags().Int64Var(&embeddingsDimensions, "dimensions", 0, "Requested output vector dimensions (0 = model default)")
	embeddingsSetCmd.Flags().BoolVar(&embeddingsNoNormular, "no-normalize", false, "Do not request unit-normalized vectors")
	embeddingsCmd.AddCommand(embeddingsListCmd, embeddingsSetCmd, embeddingsStatusCmd)
	rootCmd.AddCommand(embeddingsCmd)
}

// embeddingModelRow is one row in list/JSON output.
type embeddingModelRow struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Name       string `json:"name"`
	Dimensions int64  `json:"dimensions"`
	Default    bool   `json:"default"`
}

func embeddingModels(cfg *config.ConfigStore) []embeddingModelRow {
	var rows []embeddingModelRow
	for _, p := range cfg.KnownProviders() {
		for _, m := range p.EmbeddingModels {
			rows = append(rows, embeddingModelRow{
				Provider:   string(p.ID),
				Model:      m.ID,
				Name:       m.Name,
				Dimensions: m.Dimensions,
				Default:    m.ID == p.DefaultEmbeddingModelID,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Provider != rows[j].Provider {
			return rows[i].Provider < rows[j].Provider
		}
		return rows[i].Model < rows[j].Model
	})
	return rows
}

func runEmbeddingsList(cmd *cobra.Command, _ []string) error {
	dataDir, _ := cmd.Flags().GetString("data-dir")
	cfg, err := config.Init("", dataDir, false)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}
	rows := embeddingModels(cfg)

	if embeddingsJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		if rows == nil {
			rows = []embeddingModelRow{}
		}
		return enc.Encode(rows)
	}

	if len(rows) == 0 {
		cmd.Println("No embedding models available from known providers.")
		return nil
	}

	if term.IsTerminal(os.Stdout.Fd()) {
		t := table.New().
			Border(lipgloss.RoundedBorder()).
			StyleFunc(func(row, col int) lipgloss.Style {
				return lipgloss.NewStyle().Padding(0, 1)
			}).
			Headers("PROVIDER", "MODEL", "NAME", "DIMS", "DEFAULT")
		for _, r := range rows {
			def := ""
			if r.Default {
				def = "✓"
			}
			t.Row(r.Provider, r.Model, r.Name, fmt.Sprintf("%d", r.Dimensions), def)
		}
		lipgloss.Println(t)
		return nil
	}
	for _, r := range rows {
		cmd.Printf("%s\t%s\t%s\t%d\n", r.Provider, r.Model, r.Name, r.Dimensions)
	}
	return nil
}

func runEmbeddingsSet(cmd *cobra.Command, args []string) error {
	provider, model := args[0], args[1]
	dataDir, _ := cmd.Flags().GetString("data-dir")
	cfg, err := config.Init("", dataDir, false)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}

	ec := &config.EmbeddingConfig{
		Provider:   provider,
		Model:      model,
		Dimensions: embeddingsDimensions,
		Normalize:  !embeddingsNoNormular,
	}
	if err := ec.Validate(); err != nil {
		return err
	}

	// Warn when this changes the embedding space and there are stored
	// vectors that will be invalidated.
	old := cfg.Config().Embedding
	changed := old.Signature() != ec.Signature()

	if err := cfg.SetConfigField(config.ScopeGlobal, "embedding", ec); err != nil {
		return fmt.Errorf("failed to write embedding config: %w", err)
	}

	cmd.Printf("Embedding model set to %s/%s", provider, model)
	if ec.Dimensions > 0 {
		cmd.Printf(" (%d dims)", ec.Dimensions)
	}
	cmd.Println()
	if changed && old != nil {
		cmd.Println("Note: the embedding space changed; existing vectors will be re-indexed on next open.")
	}
	return nil
}

func runEmbeddingsStatus(cmd *cobra.Command, _ []string) error {
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
	emb := embedding.Build(queries, cfg.EmbeddingParams())
	active, total, err := emb.Counts(ctx)
	if err != nil {
		return fmt.Errorf("failed to count embeddings: %w", err)
	}

	ec := cfg.Config().Embedding
	status := struct {
		Enabled       bool   `json:"enabled"`
		Provider      string `json:"provider,omitempty"`
		Model         string `json:"model,omitempty"`
		Dimensions    int64  `json:"dimensions,omitempty"`
		HybridSearch  bool   `json:"hybrid_search"`
		Signature     string `json:"signature,omitempty"`
		ActiveVectors int64  `json:"active_vectors"`
		TotalVectors  int64  `json:"total_vectors"`
	}{
		Enabled:       emb.Enabled(),
		HybridSearch:  ec.HybridEnabled(),
		Signature:     emb.Signature(),
		ActiveVectors: active,
		TotalVectors:  total,
	}
	if ec != nil {
		status.Provider = ec.Provider
		status.Model = ec.Model
		status.Dimensions = ec.Dimensions
	}

	if embeddingsJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		return enc.Encode(status)
	}

	if !status.Enabled {
		cmd.Println("Embeddings: disabled (no embedder configured).")
		cmd.Println("Search uses substring matching only. Configure one with 'crush embeddings set'.")
		return nil
	}
	cmd.Printf("Embedder:       %s/%s\n", status.Provider, status.Model)
	if status.Dimensions > 0 {
		cmd.Printf("Dimensions:     %d\n", status.Dimensions)
	}
	cmd.Printf("Hybrid search:  %v\n", status.HybridSearch)
	cmd.Printf("Signature:      %s\n", short12(status.Signature))
	cmd.Printf("Vectors:        %d active / %d total\n", status.ActiveVectors, status.TotalVectors)
	if status.TotalVectors > status.ActiveVectors {
		cmd.Printf("                %d stale (different embedder); re-indexed on next open.\n", status.TotalVectors-status.ActiveVectors)
	}
	return nil
}

func short12(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
