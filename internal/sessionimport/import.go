package sessionimport

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/taigrr/crush/internal/message"
)

type Session struct {
	Source     Source
	SourceID   string
	Title      string
	WorkingDir string
	CreatedAt  int64
	UpdatedAt  int64
	Messages   []Message
	Warnings   []string
}

type Message struct {
	Role      message.MessageRole
	Parts     []message.ContentPart
	Model     string
	Provider  string
	CreatedAt int64
}

type Result struct {
	ID       string `json:"id"`
	Source   Source `json:"source"`
	SourceID string `json:"source_id"`
	Title    string `json:"title"`
	// Messages is the number of messages in the source transcript.
	Messages int `json:"messages"`
	// Imported is the number of messages newly written on this call
	// (the whole transcript on first import, only new tail messages on
	// a re-sync, zero when nothing changed).
	Imported int      `json:"imported"`
	Warnings []string `json:"warnings,omitempty"`
	// AlreadyExist is true when the session was already present and no
	// new messages were written.
	AlreadyExist bool `json:"already_exists,omitempty"`
	// Modified is true when the session exists but has diverged from
	// the imported transcript (e.g. the user continued it inside
	// Crush), so it was left untouched to avoid clobbering local work.
	Modified bool `json:"modified,omitempty"`
}

type rawRecord map[string]any

type rawMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Model      string          `json:"model"`
	Provider   string          `json:"provider"`
}

type rawBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	Thinking   string          `json:"thinking"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	Content    json.RawMessage `json:"content"`
	ToolUseID  string          `json:"tool_use_id"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	IsError    bool            `json:"is_error"`
}

func Import(ctx context.Context, database *sql.DB, imported Session) (Result, error) {
	if imported.SourceID == "" {
		return Result{}, errors.New("session has no source ID")
	}
	if len(imported.Messages) == 0 {
		return Result{}, errors.New("session has no importable messages")
	}
	id := importedSessionID(imported.Source, imported.SourceID)
	result := Result{
		ID:       id,
		Source:   imported.Source,
		SourceID: imported.SourceID,
		Title:    imported.Title,
		Messages: len(imported.Messages),
		Warnings: imported.Warnings,
	}

	existingIDs, lastTimestamp, exists, err := existingImportState(ctx, database, id)
	if err != nil {
		return Result{}, err
	}

	// Re-import is idempotent and consistent: the session ID and each
	// message ID are derived deterministically from the source, so a
	// second import of the same transcript reuses the same rows. On
	// re-import we only append messages the source has gained since
	// last time. If the session has diverged from what we imported —
	// the user continued it inside Crush, so it contains messages we
	// did not write, or deleted the imported ones — we leave it
	// untouched rather than clobber local work.
	//
	// Divergence is inferred purely from message IDs: imported rows use
	// deterministic IDs while Crush-authored rows use random ones, and
	// an existing session with no imported rows means the user cleared
	// it. The one case this cannot detect is deleting a strict tail of
	// imported messages without adding anything: the remainder is still
	// a valid prefix, so a later source-side growth would re-add the
	// deleted tail. That is an accepted limitation.
	if exists {
		startIndex := len(existingIDs)
		if startIndex == 0 || !isImportedPrefix(id, existingIDs) {
			result.AlreadyExist = true
			result.Modified = true
			result.Warnings = append(result.Warnings, "session was modified in Crush since import; skipped to avoid overwriting")
			return result, nil
		}
		if startIndex >= len(imported.Messages) {
			result.AlreadyExist = true
			return result, nil
		}
		inserted, err := appendMessages(ctx, database, id, imported, startIndex, lastTimestamp)
		if err != nil {
			return Result{}, err
		}
		result.Imported = inserted
		return result, nil
	}

	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("starting import: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	createdAt := imported.CreatedAt
	if createdAt == 0 {
		createdAt = time.Now().Unix()
	}
	updatedAt := max(imported.UpdatedAt, createdAt)
	title := strings.TrimSpace(imported.Title)
	if title == "" {
		title = "Imported " + string(imported.Source) + " session"
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sessions
		(id, title, message_count, prompt_tokens, completion_tokens, cost, working_dir, updated_at, created_at)
		VALUES (?, ?, 0, 0, 0, 0, ?, ?, ?)`, id, title, nullableString(imported.WorkingDir), updatedAt, createdAt)
	if err != nil {
		return Result{}, fmt.Errorf("creating imported session: %w", err)
	}

	lastInsertTimestamp := createdAt - 1
	for index, importedMessage := range imported.Messages {
		lastInsertTimestamp, err = insertMessage(ctx, tx, id, index, importedMessage, lastInsertTimestamp)
		if err != nil {
			return Result{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET updated_at = ?, created_at = ? WHERE id = ?", max(updatedAt, lastInsertTimestamp), createdAt, id); err != nil {
		return Result{}, fmt.Errorf("dating imported session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("committing import: %w", err)
	}
	result.Imported = len(imported.Messages)
	return result, nil
}

// importedSessionID derives the deterministic Crush session ID for a
// source transcript so repeated imports map to the same session.
func importedSessionID(source Source, sourceID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("crush-session-import:"+string(source)+":"+sourceID)).String()
}

// importedMessageID derives the deterministic message ID for the
// index-th message of an imported session. Messages Crush authors
// itself use random UUIDs, so this scheme also distinguishes imported
// rows from local ones.
func importedMessageID(sessionID string, index int) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, fmt.Appendf(nil, "%s:%d", sessionID, index)).String()
}

// existingImportState reports the current message IDs (ordered by
// creation), the latest message timestamp, and whether the session
// exists. The ordering matches the insert order because insertMessage
// forces strictly-increasing created_at values, which is what lets
// isImportedPrefix line up each row with its deterministic index.
func existingImportState(ctx context.Context, database *sql.DB, id string) (ids []string, lastTimestamp int64, exists bool, err error) {
	var count int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", id).Scan(&count); err != nil {
		return nil, 0, false, fmt.Errorf("checking imported session: %w", err)
	}
	if count == 0 {
		return nil, 0, false, nil
	}
	rows, err := database.QueryContext(ctx, "SELECT id, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC, id ASC", id)
	if err != nil {
		return nil, 0, false, fmt.Errorf("reading imported messages: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var messageID string
		var createdAt int64
		if err := rows.Scan(&messageID, &createdAt); err != nil {
			return nil, 0, false, fmt.Errorf("reading imported messages: %w", err)
		}
		ids = append(ids, messageID)
		if createdAt > lastTimestamp {
			lastTimestamp = createdAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("reading imported messages: %w", err)
	}
	return ids, lastTimestamp, true, nil
}

// isImportedPrefix reports whether the existing message IDs are exactly
// the deterministic imported IDs for indices 0..len(ids)-1, in order.
// Any deviation means the session was touched outside the importer.
func isImportedPrefix(sessionID string, ids []string) bool {
	for index, messageID := range ids {
		if messageID != importedMessageID(sessionID, index) {
			return false
		}
	}
	return true
}

// appendMessages writes imported messages from startIndex onward in a
// single transaction and returns the number inserted. It advances the
// session's updated_at so re-synced sessions surface as recently
// active (the message-insert trigger only maintains message_count).
func appendMessages(ctx context.Context, database *sql.DB, id string, imported Session, startIndex int, lastTimestamp int64) (int, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("starting re-sync: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	for index := startIndex; index < len(imported.Messages); index++ {
		lastTimestamp, err = insertMessage(ctx, tx, id, index, imported.Messages[index], lastTimestamp)
		if err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET updated_at = ? WHERE id = ?", max(imported.UpdatedAt, lastTimestamp), id); err != nil {
		return 0, fmt.Errorf("dating imported session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing re-sync: %w", err)
	}
	return len(imported.Messages) - startIndex, nil
}

// insertMessage writes a single imported message with a monotonic
// timestamp and returns the timestamp used.
func insertMessage(ctx context.Context, tx *sql.Tx, id string, index int, importedMessage Message, lastTimestamp int64) (int64, error) {
	parts, err := message.MarshalParts(importedMessage.Parts)
	if err != nil {
		return 0, fmt.Errorf("encoding message %d: %w", index+1, err)
	}
	timestamp := importedMessage.CreatedAt
	if timestamp <= lastTimestamp {
		timestamp = lastTimestamp + 1
	}
	messageID := importedMessageID(id, index)
	_, err = tx.ExecContext(ctx, `INSERT INTO messages
		(id, session_id, role, parts, model, provider, is_summary_message, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`, messageID, id, importedMessage.Role, string(parts), nullableString(importedMessage.Model), nullableString(importedMessage.Provider), timestamp, timestamp)
	if err != nil {
		return 0, fmt.Errorf("creating imported message %d: %w", index+1, err)
	}
	return timestamp, nil
}

func latestLeafChain(records []rawRecord, idKey, parentKey string) ([]rawRecord, error) {
	byID := make(map[string]rawRecord, len(records))
	children := make(map[string]int)
	for _, record := range records {
		id := text(record[idKey])
		if id == "" {
			continue
		}
		byID[id] = record
		if parent := text(record[parentKey]); parent != "" {
			children[parent]++
		}
	}
	var leaf rawRecord
	for id, record := range byID {
		if children[id] == 0 && (leaf == nil || parseTime(text(record["timestamp"])) > parseTime(text(leaf["timestamp"]))) {
			leaf = record
		}
	}
	chain := make([]rawRecord, 0, len(byID))
	seen := make(map[string]bool)
	for leaf != nil {
		id := text(leaf[idKey])
		if seen[id] {
			return nil, errors.New("session contains a parent cycle")
		}
		seen[id] = true
		chain = append(chain, leaf)
		leaf = byID[text(leaf[parentKey])]
	}
	slices.Reverse(chain)
	return chain, nil
}

func decodeForeignMessage(value any, timestamp int64) (Message, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return Message{}, false
	}
	var raw rawMessage
	if json.Unmarshal(data, &raw) != nil {
		return Message{}, false
	}
	role := raw.Role
	if role == "toolResult" {
		role = "tool"
	}
	if role != "user" && role != "assistant" && role != "tool" {
		return Message{}, false
	}
	parts := decodeBlocksRaw(raw.Content)
	if role == "tool" && raw.ToolCallID != "" {
		content := contentTextRaw(raw.Content)
		parts = []message.ContentPart{message.ToolResult{ToolCallID: raw.ToolCallID, Name: raw.ToolName, Content: content}}
	}
	parts = filterSafeParts(parts)
	if len(parts) == 0 {
		return Message{}, false
	}
	return Message{
		Role:      message.MessageRole(role),
		Parts:     append(parts, finish()),
		Model:     raw.Model,
		Provider:  raw.Provider,
		CreatedAt: timestamp,
	}, true
}

func decodeBlocks(value any) []message.ContentPart {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return decodeBlocksRaw(data)
}

func decodeBlocksRaw(data json.RawMessage) []message.ContentPart {
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	var plain string
	if json.Unmarshal(data, &plain) == nil {
		return []message.ContentPart{message.TextContent{Text: plain}}
	}
	var blocks []rawBlock
	if json.Unmarshal(data, &blocks) != nil {
		return nil
	}
	parts := make([]message.ContentPart, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text", "input_text", "output_text":
			if block.Text != "" {
				parts = append(parts, message.TextContent{Text: block.Text})
			}
		case "tool_use", "toolCall":
			parts = append(parts, message.ToolCall{ID: block.ID, Name: block.Name, Input: stringOrJSON(block.Input), Finished: true})
		case "tool_result":
			parts = append(parts, message.ToolResult{ToolCallID: block.ToolUseID, Content: contentTextRaw(block.Content), IsError: block.IsError})
		}
	}
	return parts
}

func filterSafeParts(parts []message.ContentPart) []message.ContentPart {
	filtered := make([]message.ContentPart, 0, len(parts))
	for _, part := range parts {
		if content, ok := part.(message.TextContent); ok {
			trimmed := strings.TrimSpace(content.Text)
			if isGeneratedContext(trimmed) {
				continue
			}
		}
		filtered = append(filtered, part)
	}
	return filtered
}

func isGeneratedMessage(value any) bool {
	return isGeneratedContent(object(value)["content"])
}

func isGeneratedContent(value any) bool {
	data, err := json.Marshal(value)
	if err != nil {
		return true
	}
	return isGeneratedContext(strings.TrimSpace(contentTextRaw(data)))
}

func isGeneratedContext(value string) bool {
	prefixes := []string{
		"<system-reminder>",
		"<system_reminder>",
		"<environment_context>",
		"<user_instructions>",
		"<local-command-caveat>",
		"<local-command-stdout>",
		"<local-command-stderr>",
		"<command-name>",
		"<command-message>",
		"<user_info>",
		"<INSTRUCTIONS>",
		"# AGENTS.md instructions for ",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func readJSONL(path string) ([]rawRecord, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	var source io.Reader = file
	if strings.HasSuffix(path, ".zst") {
		decoder, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20))
		if err != nil {
			return nil, 0, fmt.Errorf("opening compressed session: %w", err)
		}
		defer decoder.Close()
		source = decoder
	}
	reader := bufio.NewReader(source)
	var records []rawRecord
	malformed := 0
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var record rawRecord
			if json.Unmarshal(line, &record) != nil {
				malformed++
			} else {
				records = append(records, record)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, malformed, readErr
		}
	}
	return records, malformed, nil
}

func setSessionMetadata(imported *Session) {
	for _, msg := range imported.Messages {
		if msg.CreatedAt == 0 {
			continue
		}
		if imported.CreatedAt == 0 || msg.CreatedAt < imported.CreatedAt {
			imported.CreatedAt = msg.CreatedAt
		}
		if msg.CreatedAt > imported.UpdatedAt {
			imported.UpdatedAt = msg.CreatedAt
		}
	}
}

func firstUserText(messages []Message) string {
	for _, msg := range messages {
		if msg.Role != message.User {
			continue
		}
		for _, part := range msg.Parts {
			if content, ok := part.(message.TextContent); ok {
				return truncate(strings.Join(strings.Fields(content.Text), " "), 200)
			}
		}
	}
	return ""
}

func dropUserTurns(messages *[]Message, count int) {
	if count <= 0 {
		return
	}
	positions := make([]int, 0)
	for index, msg := range *messages {
		if msg.Role == message.User {
			positions = append(positions, index)
		}
	}
	if len(positions) == 0 {
		*messages = nil
		return
	}
	cut := positions[max(0, len(positions)-count)]
	*messages = (*messages)[:cut]
}

func contentTextRaw(data json.RawMessage) string {
	var plain string
	if json.Unmarshal(data, &plain) == nil {
		return plain
	}
	var blocks []rawBlock
	if json.Unmarshal(data, &blocks) == nil {
		texts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			if block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return stringOrJSON(data)
}

func stringOrJSON(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return string(data)
	}
	return jsonText(value)
}

func jsonText(value any) string {
	if value == nil {
		return ""
	}
	if textValue, ok := value.(string); ok {
		return textValue
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func object(value any) rawRecord {
	if record, ok := value.(map[string]any); ok {
		return record
	}
	return rawRecord{}
}

func text(value any) string {
	result, _ := value.(string)
	return result
}

func number(value any) float64 {
	result, _ := value.(float64)
	return result
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func parseTime(value string) int64 {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return parsed.Unix()
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func finish() message.Finish {
	return message.Finish{Reason: message.FinishReasonEndTurn}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
