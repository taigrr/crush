package cmd

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/projects"
)

//go:embed stats/index.html
var statsTemplate string

//go:embed stats/index.css
var statsCSS string

//go:embed stats/index.js
var statsJS string

//go:embed stats/header.svg
var headerSVG string

//go:embed stats/heartbit.svg
var heartbitSVG string

//go:embed stats/footer.svg
var footerSVG string

var statsCmd = &cobra.Command{
	Use:   "stats",
	Short: "Show usage statistics",
	Long:  "Generate and display usage statistics including token usage, costs, and activity patterns",
	RunE:  runStats,
}

func init() {
	statsCmd.Flags().Bool("all", false, "Aggregate stats across all registered workspaces")
}

// Day names for day of week statistics.
var dayNames = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// Stats holds all the statistics data.
type Stats struct {
	GeneratedAt       time.Time          `json:"generated_at"`
	Total             TotalStats         `json:"total"`
	UsageByDay        []DailyUsage       `json:"usage_by_day"`
	UsageByModel      []ModelUsage       `json:"usage_by_model"`
	UsageByHour       []HourlyUsage      `json:"usage_by_hour"`
	UsageByDayOfWeek  []DayOfWeekUsage   `json:"usage_by_day_of_week"`
	RecentActivity    []DailyActivity    `json:"recent_activity"`
	AvgResponseTimeMs float64            `json:"avg_response_time_ms"`
	ToolUsage         []ToolUsage        `json:"tool_usage"`
	HourDayHeatmap    []HourDayHeatmapPt `json:"hour_day_heatmap"`
	Workspaces        []WorkspaceStats   `json:"workspaces,omitempty"`
}

// WorkspaceStats holds per-workspace totals for the --all breakdown.
type WorkspaceStats struct {
	Path    string     `json:"path"`
	DataDir string     `json:"data_dir"`
	Total   TotalStats `json:"total"`
}

type TotalStats struct {
	TotalSessions         int64   `json:"total_sessions"`
	TotalPromptTokens     int64   `json:"total_prompt_tokens"`
	TotalCompletionTokens int64   `json:"total_completion_tokens"`
	TotalTokens           int64   `json:"total_tokens"`
	TotalCost             float64 `json:"total_cost"`
	TotalMessages         int64   `json:"total_messages"`
	AvgTokensPerSession   float64 `json:"avg_tokens_per_session"`
	AvgMessagesPerSession float64 `json:"avg_messages_per_session"`
}

type DailyUsage struct {
	Day              string  `json:"day"`
	PromptTokens     int64   `json:"prompt_tokens"`
	CompletionTokens int64   `json:"completion_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	Cost             float64 `json:"cost"`
	SessionCount     int64   `json:"session_count"`
}

type ModelUsage struct {
	Model        string `json:"model"`
	Provider     string `json:"provider"`
	MessageCount int64  `json:"message_count"`
}

type HourlyUsage struct {
	Hour         int   `json:"hour"`
	SessionCount int64 `json:"session_count"`
}

type DayOfWeekUsage struct {
	DayOfWeek        int    `json:"day_of_week"`
	DayName          string `json:"day_name"`
	SessionCount     int64  `json:"session_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

type DailyActivity struct {
	Day          string  `json:"day"`
	SessionCount int64   `json:"session_count"`
	TotalTokens  int64   `json:"total_tokens"`
	Cost         float64 `json:"cost"`
}

type ToolUsage struct {
	ToolName  string `json:"tool_name"`
	CallCount int64  `json:"call_count"`
}

type HourDayHeatmapPt struct {
	DayOfWeek    int   `json:"day_of_week"`
	Hour         int   `json:"hour"`
	SessionCount int64 `json:"session_count"`
}

func runStats(cmd *cobra.Command, _ []string) error {
	dataDir, _ := cmd.Flags().GetString("data-dir")
	allWorkspaces, _ := cmd.Flags().GetBool("all")
	ctx := cmd.Context()

	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return err
	}
	cfg, err := config.Init(cwd, dataDir, false)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}
	if dataDir == "" {
		dataDir = cfg.Config().Options.DataDirectory
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}
	username := currentUser.Username

	var stats *Stats
	var project string
	if allWorkspaces {
		stats, err = gatherAllWorkspaceStats(ctx, currentUser.HomeDir)
		if err != nil {
			return err
		}
		project = "all workspaces"
	} else {
		conn, err := db.Connect(ctx, dataDir)
		if err != nil {
			return fmt.Errorf("failed to connect to database: %w", err)
		}
		defer conn.Close()

		stats, err = gatherStats(ctx, conn)
		if err != nil {
			return fmt.Errorf("failed to gather stats: %w", err)
		}
		project, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		project = strings.Replace(project, currentUser.HomeDir, "~", 1)
	}

	if stats.Total.TotalSessions == 0 {
		return fmt.Errorf("no data available: no sessions found in database")
	}

	htmlPath := filepath.Join(dataDir, "stats/index.html")
	if err := generateHTML(stats, project, username, htmlPath); err != nil {
		return fmt.Errorf("failed to generate HTML: %w", err)
	}

	fmt.Printf("Stats generated: %s\n", htmlPath)

	if err := browser.OpenFile(htmlPath); err != nil {
		fmt.Printf("Could not open browser: %v\n", err)
		fmt.Println("Please open the file manually.")
	}

	return nil
}

// gatherAllWorkspaceStats aggregates stats across every registered workspace,
// producing a combined Stats plus a per-workspace breakdown.
func gatherAllWorkspaceStats(ctx context.Context, homeDir string) (*Stats, error) {
	projectList, err := projects.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	combined := &Stats{GeneratedAt: time.Now()}
	// Weighted accumulator for average response time (by message count).
	var respTimeWeighted, respTimeWeight float64

	seen := make(map[string]bool)
	for _, p := range projectList {
		if p.DataDir == "" || seen[p.DataDir] {
			continue
		}
		seen[p.DataDir] = true

		conn, err := db.Connect(ctx, p.DataDir)
		if err != nil {
			fmt.Printf("Skipping %s: %v\n", p.Path, err)
			continue
		}
		ws, err := gatherStats(ctx, conn)
		conn.Close()
		if err != nil {
			fmt.Printf("Skipping %s: %v\n", p.Path, err)
			continue
		}
		if ws.Total.TotalSessions == 0 {
			continue
		}

		mergeStats(combined, ws)
		respTimeWeighted += ws.AvgResponseTimeMs * float64(ws.Total.TotalMessages)
		respTimeWeight += float64(ws.Total.TotalMessages)

		combined.Workspaces = append(combined.Workspaces, WorkspaceStats{
			Path:    strings.Replace(p.Path, homeDir, "~", 1),
			DataDir: p.DataDir,
			Total:   ws.Total,
		})
	}

	if respTimeWeight > 0 {
		combined.AvgResponseTimeMs = respTimeWeighted / respTimeWeight
	}
	finalizeTotals(combined)
	sortAggregates(combined)
	return combined, nil
}

// mergeStats adds all of src's aggregatable data into dst.
func mergeStats(dst, src *Stats) {
	dst.Total.TotalSessions += src.Total.TotalSessions
	dst.Total.TotalPromptTokens += src.Total.TotalPromptTokens
	dst.Total.TotalCompletionTokens += src.Total.TotalCompletionTokens
	dst.Total.TotalTokens += src.Total.TotalTokens
	dst.Total.TotalCost += src.Total.TotalCost
	dst.Total.TotalMessages += src.Total.TotalMessages

	dst.UsageByDay = mergeUsageByDay(dst.UsageByDay, src.UsageByDay)
	dst.UsageByModel = mergeUsageByModel(dst.UsageByModel, src.UsageByModel)
	dst.UsageByHour = mergeUsageByHour(dst.UsageByHour, src.UsageByHour)
	dst.UsageByDayOfWeek = mergeUsageByDayOfWeek(dst.UsageByDayOfWeek, src.UsageByDayOfWeek)
	dst.RecentActivity = mergeRecentActivity(dst.RecentActivity, src.RecentActivity)
	dst.ToolUsage = mergeToolUsage(dst.ToolUsage, src.ToolUsage)
	dst.HourDayHeatmap = mergeHeatmap(dst.HourDayHeatmap, src.HourDayHeatmap)
}

func mergeUsageByDay(dst, src []DailyUsage) []DailyUsage {
	idx := make(map[string]int, len(dst))
	for i, d := range dst {
		idx[d.Day] = i
	}
	for _, s := range src {
		if i, ok := idx[s.Day]; ok {
			dst[i].PromptTokens += s.PromptTokens
			dst[i].CompletionTokens += s.CompletionTokens
			dst[i].TotalTokens += s.TotalTokens
			dst[i].Cost += s.Cost
			dst[i].SessionCount += s.SessionCount
			continue
		}
		idx[s.Day] = len(dst)
		dst = append(dst, s)
	}
	return dst
}

func mergeUsageByModel(dst, src []ModelUsage) []ModelUsage {
	idx := make(map[string]int, len(dst))
	for i, m := range dst {
		idx[m.Provider+"\x00"+m.Model] = i
	}
	for _, s := range src {
		key := s.Provider + "\x00" + s.Model
		if i, ok := idx[key]; ok {
			dst[i].MessageCount += s.MessageCount
			continue
		}
		idx[key] = len(dst)
		dst = append(dst, s)
	}
	return dst
}

func mergeUsageByHour(dst, src []HourlyUsage) []HourlyUsage {
	idx := make(map[int]int, len(dst))
	for i, h := range dst {
		idx[h.Hour] = i
	}
	for _, s := range src {
		if i, ok := idx[s.Hour]; ok {
			dst[i].SessionCount += s.SessionCount
			continue
		}
		idx[s.Hour] = len(dst)
		dst = append(dst, s)
	}
	return dst
}

func mergeUsageByDayOfWeek(dst, src []DayOfWeekUsage) []DayOfWeekUsage {
	idx := make(map[int]int, len(dst))
	for i, d := range dst {
		idx[d.DayOfWeek] = i
	}
	for _, s := range src {
		if i, ok := idx[s.DayOfWeek]; ok {
			dst[i].SessionCount += s.SessionCount
			dst[i].PromptTokens += s.PromptTokens
			dst[i].CompletionTokens += s.CompletionTokens
			continue
		}
		idx[s.DayOfWeek] = len(dst)
		dst = append(dst, s)
	}
	return dst
}

func mergeRecentActivity(dst, src []DailyActivity) []DailyActivity {
	idx := make(map[string]int, len(dst))
	for i, d := range dst {
		idx[d.Day] = i
	}
	for _, s := range src {
		if i, ok := idx[s.Day]; ok {
			dst[i].SessionCount += s.SessionCount
			dst[i].TotalTokens += s.TotalTokens
			dst[i].Cost += s.Cost
			continue
		}
		idx[s.Day] = len(dst)
		dst = append(dst, s)
	}
	return dst
}

func mergeToolUsage(dst, src []ToolUsage) []ToolUsage {
	idx := make(map[string]int, len(dst))
	for i, t := range dst {
		idx[t.ToolName] = i
	}
	for _, s := range src {
		if i, ok := idx[s.ToolName]; ok {
			dst[i].CallCount += s.CallCount
			continue
		}
		idx[s.ToolName] = len(dst)
		dst = append(dst, s)
	}
	return dst
}

func mergeHeatmap(dst, src []HourDayHeatmapPt) []HourDayHeatmapPt {
	idx := make(map[int]int, len(dst))
	for i, h := range dst {
		idx[h.DayOfWeek*100+h.Hour] = i
	}
	for _, s := range src {
		key := s.DayOfWeek*100 + s.Hour
		if i, ok := idx[key]; ok {
			dst[i].SessionCount += s.SessionCount
			continue
		}
		idx[key] = len(dst)
		dst = append(dst, s)
	}
	return dst
}

// finalizeTotals recomputes derived averages from merged totals.
func finalizeTotals(s *Stats) {
	if s.Total.TotalSessions > 0 {
		s.Total.AvgTokensPerSession = float64(s.Total.TotalTokens) / float64(s.Total.TotalSessions)
		s.Total.AvgMessagesPerSession = float64(s.Total.TotalMessages) / float64(s.Total.TotalSessions)
	}
}

// sortAggregates orders merged slices for stable, meaningful presentation.
func sortAggregates(s *Stats) {
	slices.SortFunc(s.UsageByDay, func(a, b DailyUsage) int { return strings.Compare(b.Day, a.Day) })
	slices.SortFunc(s.RecentActivity, func(a, b DailyActivity) int { return strings.Compare(a.Day, b.Day) })
	slices.SortFunc(s.UsageByModel, func(a, b ModelUsage) int { return int(b.MessageCount - a.MessageCount) })
	slices.SortFunc(s.ToolUsage, func(a, b ToolUsage) int { return int(b.CallCount - a.CallCount) })
	slices.SortFunc(s.UsageByHour, func(a, b HourlyUsage) int { return a.Hour - b.Hour })
	slices.SortFunc(s.UsageByDayOfWeek, func(a, b DayOfWeekUsage) int { return a.DayOfWeek - b.DayOfWeek })
	slices.SortFunc(s.Workspaces, func(a, b WorkspaceStats) int {
		if a.Total.TotalCost > b.Total.TotalCost {
			return -1
		}
		if a.Total.TotalCost < b.Total.TotalCost {
			return 1
		}
		return 0
	})
}

func gatherStats(ctx context.Context, conn *sql.DB) (*Stats, error) {
	queries := db.New(conn)

	stats := &Stats{
		GeneratedAt: time.Now(),
	}

	// Total stats.
	total, err := queries.GetTotalStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("get total stats: %w", err)
	}
	stats.Total = TotalStats{
		TotalSessions:         total.TotalSessions,
		TotalPromptTokens:     toInt64(total.TotalPromptTokens),
		TotalCompletionTokens: toInt64(total.TotalCompletionTokens),
		TotalTokens:           toInt64(total.TotalPromptTokens) + toInt64(total.TotalCompletionTokens),
		TotalCost:             toFloat64(total.TotalCost),
		TotalMessages:         toInt64(total.TotalMessages),
		AvgTokensPerSession:   toFloat64(total.AvgTokensPerSession),
		AvgMessagesPerSession: toFloat64(total.AvgMessagesPerSession),
	}

	// Usage by day.
	dailyUsage, err := queries.GetUsageByDay(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by day: %w", err)
	}
	for _, d := range dailyUsage {
		prompt := nullFloat64ToInt64(d.PromptTokens)
		completion := nullFloat64ToInt64(d.CompletionTokens)
		stats.UsageByDay = append(stats.UsageByDay, DailyUsage{
			Day:              fmt.Sprintf("%v", d.Day),
			PromptTokens:     prompt,
			CompletionTokens: completion,
			TotalTokens:      prompt + completion,
			Cost:             d.Cost.Float64,
			SessionCount:     d.SessionCount,
		})
	}

	// Usage by model.
	modelUsage, err := queries.GetUsageByModel(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by model: %w", err)
	}
	for _, m := range modelUsage {
		stats.UsageByModel = append(stats.UsageByModel, ModelUsage{
			Model:        m.Model,
			Provider:     m.Provider,
			MessageCount: m.MessageCount,
		})
	}

	// Usage by hour.
	hourlyUsage, err := queries.GetUsageByHour(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by hour: %w", err)
	}
	for _, h := range hourlyUsage {
		stats.UsageByHour = append(stats.UsageByHour, HourlyUsage{
			Hour:         int(h.Hour),
			SessionCount: h.SessionCount,
		})
	}

	// Usage by day of week.
	dowUsage, err := queries.GetUsageByDayOfWeek(ctx)
	if err != nil {
		return nil, fmt.Errorf("get usage by day of week: %w", err)
	}
	for _, d := range dowUsage {
		stats.UsageByDayOfWeek = append(stats.UsageByDayOfWeek, DayOfWeekUsage{
			DayOfWeek:        int(d.DayOfWeek),
			DayName:          dayNames[int(d.DayOfWeek)],
			SessionCount:     d.SessionCount,
			PromptTokens:     nullFloat64ToInt64(d.PromptTokens),
			CompletionTokens: nullFloat64ToInt64(d.CompletionTokens),
		})
	}

	// Recent activity (last 30 days).
	recent, err := queries.GetRecentActivity(ctx)
	if err != nil {
		return nil, fmt.Errorf("get recent activity: %w", err)
	}
	for _, r := range recent {
		stats.RecentActivity = append(stats.RecentActivity, DailyActivity{
			Day:          fmt.Sprintf("%v", r.Day),
			SessionCount: r.SessionCount,
			TotalTokens:  nullFloat64ToInt64(r.TotalTokens),
			Cost:         r.Cost.Float64,
		})
	}

	// Average response time.
	avgResp, err := queries.GetAverageResponseTime(ctx)
	if err != nil {
		return nil, fmt.Errorf("get average response time: %w", err)
	}
	stats.AvgResponseTimeMs = toFloat64(avgResp) * 1000

	// Tool usage.
	toolUsage, err := queries.GetToolUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("get tool usage: %w", err)
	}
	for _, t := range toolUsage {
		if name, ok := t.ToolName.(string); ok && name != "" {
			stats.ToolUsage = append(stats.ToolUsage, ToolUsage{
				ToolName:  name,
				CallCount: t.CallCount,
			})
		}
	}

	// Hour/day heatmap.
	heatmap, err := queries.GetHourDayHeatmap(ctx)
	if err != nil {
		return nil, fmt.Errorf("get hour day heatmap: %w", err)
	}
	for _, h := range heatmap {
		stats.HourDayHeatmap = append(stats.HourDayHeatmap, HourDayHeatmapPt{
			DayOfWeek:    int(h.DayOfWeek),
			Hour:         int(h.Hour),
			SessionCount: h.SessionCount,
		})
	}

	return stats, nil
}

func toInt64(v any) int64 {
	switch val := v.(type) {
	case int64:
		return val
	case float64:
		return int64(val)
	case int:
		return int64(val)
	default:
		return 0
	}
}

func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int64:
		return float64(val)
	case int:
		return float64(val)
	default:
		return 0
	}
}

func nullFloat64ToInt64(n sql.NullFloat64) int64 {
	if n.Valid {
		return int64(n.Float64)
	}
	return 0
}

func generateHTML(stats *Stats, projName, username, path string) error {
	statsJSON, err := json.Marshal(stats)
	if err != nil {
		return err
	}

	tmpl, err := template.New("stats").Parse(statsTemplate)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}

	data := struct {
		StatsJSON   template.JS
		CSS         template.CSS
		JS          template.JS
		Header      template.HTML
		Heartbit    template.HTML
		Footer      template.HTML
		GeneratedAt string
		ProjectName string
		Username    string
	}{
		StatsJSON:   template.JS(statsJSON),
		CSS:         template.CSS(statsCSS),
		JS:          template.JS(statsJS),
		Header:      template.HTML(headerSVG),
		Heartbit:    template.HTML(heartbitSVG),
		Footer:      template.HTML(footerSVG),
		GeneratedAt: stats.GeneratedAt.Format("2006-01-02"),
		ProjectName: projName,
		Username:    username,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	return os.WriteFile(path, buf.Bytes(), 0o644)
}
