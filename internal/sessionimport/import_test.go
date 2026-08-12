package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/message"
)

func TestParseClaudeUsesActiveBranchAndFiltersInstructions(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "claude.jsonl", `
{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"claude-id","cwd":"/repo","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"claude-id","cwd":"/repo","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret"},{"type":"text","text":"hi"},{"type":"tool_use","id":"call-1","name":"view","input":{"path":"README.md"}}]}}
{"type":"user","uuid":"u2","parentUuid":"a1","sessionId":"claude-id","cwd":"/repo","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":"<system-reminder>ignore this</system-reminder>"}}
{"type":"assistant","uuid":"side","parentUuid":"u1","sessionId":"claude-id","isSidechain":true,"timestamp":"2026-01-01T00:00:03Z","message":{"role":"assistant","content":"sidechain"}}
{"type":"custom-title","customTitle":"Claude title"}
`)

	imported, err := Parse(path, SourceAuto)
	require.NoError(t, err)
	require.Equal(t, SourceClaude, imported.Source)
	require.Equal(t, "claude", imported.SourceID)
	require.Equal(t, "Claude title", imported.Title)
	require.Equal(t, "/repo", imported.WorkingDir)
	require.Len(t, imported.Messages, 2)
	require.Equal(t, message.User, imported.Messages[0].Role)
	require.Equal(t, message.Assistant, imported.Messages[1].Role)
	require.Contains(t, imported.Messages[1].Parts, message.ToolCall{ID: "call-1", Name: "view", Input: `{"path":"README.md"}`, Finished: true})
}

func TestParseCodexCompactionAndRollback(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "rollout-2026-01-01T00-00-00-11111111-1111-1111-1111-111111111111.jsonl", `
{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"11111111-1111-1111-1111-111111111111","cwd":"/repo"}}
{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"discarded"}]}}
{"timestamp":"2026-01-01T00:00:02Z","type":"compacted","payload":{"replacement_history":[{"type":"message","role":"user","content":[{"type":"input_text","text":"kept"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}}
{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"rolled back"}]}}
{"timestamp":"2026-01-01T00:00:04Z","type":"event_msg","payload":{"type":"thread_rolled_back","num_turns":1}}
{"timestamp":"2026-01-01T00:00:05Z","type":"response_item","payload":{"type":"function_call","call_id":"c1","name":"shell","arguments":"{}"}}
{"timestamp":"2026-01-01T00:00:06Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"ok"}}
`)

	imported, err := Parse(path, SourceAuto)
	require.NoError(t, err)
	require.Equal(t, SourceCodex, imported.Source)
	require.Equal(t, "11111111-1111-1111-1111-111111111111", imported.SourceID)
	require.Len(t, imported.Messages, 4)
	require.Equal(t, "kept", imported.Title)
	require.Equal(t, message.User, imported.Messages[0].Role)
	require.Equal(t, message.Tool, imported.Messages[3].Role)
}

func TestParseGrokDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "summary.json"), []byte(`{"info":{"id":"grok-id","cwd":"/repo"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:02Z","session_summary":"Grok title"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte("{\"type\":\"system\",\"content\":\"instructions\"}\n{\"type\":\"user\",\"content\":\"hello\"}\n{\"type\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"hi\"}]}\n"), 0o600))

	imported, err := Parse(dir, SourceAuto)
	require.NoError(t, err)
	require.Equal(t, SourceGrok, imported.Source)
	require.Equal(t, "grok-id", imported.SourceID)
	require.Equal(t, "Grok title", imported.Title)
	require.Len(t, imported.Messages, 2)
}

func TestParsePiFollowsLatestBranchAndTracksModel(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "pi.jsonl", `
{"type":"session","version":3,"id":"pi-id","timestamp":"2026-01-01T00:00:00Z","cwd":"/repo"}
{"type":"model_change","id":"m1","parentId":null,"timestamp":"2026-01-01T00:00:01Z","provider":"openai","modelId":"gpt-test"}
{"type":"message","id":"u1","parentId":"m1","timestamp":"2026-01-01T00:00:02Z","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}
{"type":"message","id":"old","parentId":"u1","timestamp":"2026-01-01T00:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"old branch"}]}}
{"type":"message","id":"new","parentId":"u1","timestamp":"2026-01-01T00:00:04Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"secret"},{"type":"text","text":"new branch"}]}}
`)

	imported, err := Parse(path, SourceAuto)
	require.NoError(t, err)
	require.Equal(t, SourcePi, imported.Source)
	require.Equal(t, "pi-id", imported.SourceID)
	require.Len(t, imported.Messages, 2)
	require.Equal(t, "gpt-test", imported.Messages[1].Model)
	require.Equal(t, "openai", imported.Messages[1].Provider)
	require.Equal(t, "hello", imported.Title)
}

func TestImportIsTransactionalAndIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(db.ResetPool)
	database, err := db.Connect(context.Background(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Release(dataDir)) })

	imported := Session{
		Source:     SourcePi,
		SourceID:   "pi-id",
		Title:      "Imported",
		WorkingDir: "/repo",
		CreatedAt:  100,
		UpdatedAt:  101,
		Messages: []Message{
			{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello"}, finish()}, CreatedAt: 100},
			{Role: message.Assistant, Parts: []message.ContentPart{message.TextContent{Text: "hi"}, finish()}, CreatedAt: 101},
		},
	}

	first, err := Import(context.Background(), database, imported)
	require.NoError(t, err)
	require.False(t, first.AlreadyExist)
	second, err := Import(context.Background(), database, imported)
	require.NoError(t, err)
	require.True(t, second.AlreadyExist)
	require.Equal(t, first.ID, second.ID)

	var count int
	require.NoError(t, database.QueryRow("SELECT message_count FROM sessions WHERE id = ?", first.ID).Scan(&count))
	require.Equal(t, 2, count)
	rows, err := message.NewService(db.New(database)).List(context.Background(), first.ID)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, int64(100), rows[0].CreatedAt)
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(content[1:]), 0o600))
	return path
}
