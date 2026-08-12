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
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/taigrr/crush/internal/message"
)

type Source string

const (
	SourceAuto   Source = "auto"
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
	SourceGrok   Source = "grok"
	SourcePi     Source = "pi"
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
	ID           string   `json:"id"`
	Source       Source   `json:"source"`
	SourceID     string   `json:"source_id"`
	Title        string   `json:"title"`
	Messages     int      `json:"messages"`
	Warnings     []string `json:"warnings,omitempty"`
	AlreadyExist bool     `json:"already_exists,omitempty"`
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

func Parse(path string, source Source) (Session, error) {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return Session{}, err
	}
	if source == SourceAuto {
		source, resolved, err = detectSource(resolved)
		if err != nil {
			return Session{}, err
		}
	}
	switch source {
	case SourceClaude:
		return parseClaude(resolved)
	case SourceCodex:
		return parseCodex(resolved)
	case SourceGrok:
		return parseGrok(resolved)
	case SourcePi:
		return parsePi(resolved)
	default:
		return Session{}, fmt.Errorf("unsupported session source %q", source)
	}
}

func Import(ctx context.Context, database *sql.DB, imported Session) (Result, error) {
	if imported.SourceID == "" {
		return Result{}, errors.New("session has no source ID")
	}
	if len(imported.Messages) == 0 {
		return Result{}, errors.New("session has no importable messages")
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("crush-session-import:"+string(imported.Source)+":"+imported.SourceID)).String()
	result := Result{
		ID:       id,
		Source:   imported.Source,
		SourceID: imported.SourceID,
		Title:    imported.Title,
		Messages: len(imported.Messages),
		Warnings: imported.Warnings,
	}
	var exists int
	if err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id = ?", id).Scan(&exists); err != nil {
		return Result{}, fmt.Errorf("checking imported session: %w", err)
	}
	if exists > 0 {
		result.AlreadyExist = true
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
	updatedAt := imported.UpdatedAt
	if updatedAt < createdAt {
		updatedAt = createdAt
	}
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

	lastTimestamp := createdAt - 1
	for index, importedMessage := range imported.Messages {
		parts, err := message.MarshalParts(importedMessage.Parts)
		if err != nil {
			return Result{}, fmt.Errorf("encoding message %d: %w", index+1, err)
		}
		timestamp := importedMessage.CreatedAt
		if timestamp <= lastTimestamp {
			timestamp = lastTimestamp + 1
		}
		lastTimestamp = timestamp
		messageID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(fmt.Sprintf("%s:%d", id, index))).String()
		_, err = tx.ExecContext(ctx, `INSERT INTO messages
			(id, session_id, role, parts, model, provider, is_summary_message, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`, messageID, id, importedMessage.Role, string(parts), nullableString(importedMessage.Model), nullableString(importedMessage.Provider), timestamp, timestamp)
		if err != nil {
			return Result{}, fmt.Errorf("creating imported message %d: %w", index+1, err)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sessions SET updated_at = ?, created_at = ? WHERE id = ?", max(updatedAt, lastTimestamp), createdAt, id); err != nil {
		return Result{}, fmt.Errorf("dating imported session: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("committing import: %w", err)
	}
	return result, nil
}

func detectSource(path string) (Source, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	if info.IsDir() {
		if fileExists(filepath.Join(path, "summary.json")) && (fileExists(filepath.Join(path, "updates.jsonl")) || fileExists(filepath.Join(path, "chat_history.jsonl"))) {
			return SourceGrok, path, nil
		}
		return "", "", fmt.Errorf("cannot detect session format from directory %s", path)
	}
	records, _, err := readJSONL(path)
	if err != nil {
		return "", "", err
	}
	if len(records) == 0 {
		return "", "", fmt.Errorf("session file %s is empty", path)
	}
	first := records[0]
	if first["type"] == "session" && number(first["version"]) > 0 {
		return SourcePi, path, nil
	}
	if first["type"] == "session_meta" || first["type"] == "turn_context" {
		return SourceCodex, path, nil
	}
	if _, ok := first["sessionId"]; ok {
		return SourceClaude, path, nil
	}
	if _, ok := first["uuid"]; ok {
		return SourceClaude, path, nil
	}
	if first["type"] == "system" || first["type"] == "user" || first["type"] == "assistant" {
		return SourceGrok, path, nil
	}
	return "", "", fmt.Errorf("cannot detect session format from %s", path)
}

func parseClaude(path string) (Session, error) {
	records, malformed, err := readJSONL(path)
	if err != nil {
		return Session{}, err
	}
	byID := make(map[string]rawRecord)
	children := make(map[string]int)
	for _, record := range records {
		id := text(record["uuid"])
		if id == "" || boolValue(record["isSidechain"]) {
			continue
		}
		kind := text(record["type"])
		if kind != "user" && kind != "assistant" {
			continue
		}
		byID[id] = record
		if parent := text(record["parentUuid"]); parent != "" {
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
		id := text(leaf["uuid"])
		if seen[id] {
			return Session{}, errors.New("Claude session contains a parent cycle")
		}
		seen[id] = true
		chain = append(chain, leaf)
		leaf = byID[text(leaf["parentUuid"])]
	}
	slices.Reverse(chain)
	imported := Session{Source: SourceClaude, SourceID: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	if malformed > 0 {
		imported.Warnings = append(imported.Warnings, fmt.Sprintf("skipped %d malformed record(s)", malformed))
	}
	for _, record := range chain {
		if imported.WorkingDir == "" {
			imported.WorkingDir = text(record["cwd"])
		}
		timestamp := parseTime(text(record["timestamp"]))
		msg, ok := decodeForeignMessage(record["message"], timestamp)
		if !ok {
			continue
		}
		imported.Messages = append(imported.Messages, msg)
	}
	setSessionMetadata(&imported)
	imported.Title = claudeTitle(records, imported.Messages)
	return imported, nil
}

func parseCodex(path string) (Session, error) {
	records, malformed, err := readJSONL(path)
	if err != nil {
		return Session{}, err
	}
	imported := Session{Source: SourceCodex}
	if malformed > 0 {
		imported.Warnings = append(imported.Warnings, fmt.Sprintf("skipped %d malformed record(s)", malformed))
	}
	start := 0
	var base []any
	for index, record := range records {
		payload := object(record["payload"])
		if record["type"] == "session_meta" && imported.SourceID == "" {
			imported.SourceID = text(payload["id"])
			imported.WorkingDir = text(payload["cwd"])
		}
		if record["type"] == "compacted" {
			if replacement, ok := payload["replacement_history"].([]any); ok {
				base = replacement
				start = index + 1
			}
		}
	}
	for _, item := range base {
		appendCodexItem(&imported, object(item), 0)
	}
	for _, record := range records[start:] {
		payload := object(record["payload"])
		switch record["type"] {
		case "response_item":
			appendCodexItem(&imported, payload, parseTime(text(record["timestamp"])))
		case "event_msg":
			if payload["type"] == "thread_rolled_back" {
				dropUserTurns(&imported.Messages, int(number(payload["num_turns"])))
			}
		}
	}
	if imported.SourceID == "" {
		imported.SourceID = codexIDFromPath(path)
	}
	setSessionMetadata(&imported)
	imported.Title = firstUserText(imported.Messages)
	return imported, nil
}

func parseGrok(path string) (Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Session{}, err
	}
	directory := filepath.Dir(path)
	updates := false
	if info.IsDir() {
		directory = path
		if fileExists(filepath.Join(path, "updates.jsonl")) {
			path = filepath.Join(path, "updates.jsonl")
			updates = true
		} else {
			path = filepath.Join(path, "chat_history.jsonl")
		}
	} else if filepath.Base(path) == "updates.jsonl" {
		updates = true
	}
	records, malformed, err := readJSONL(path)
	if err != nil {
		return Session{}, err
	}
	imported := Session{Source: SourceGrok, SourceID: filepath.Base(directory)}
	if malformed > 0 {
		imported.Warnings = append(imported.Warnings, fmt.Sprintf("skipped %d malformed record(s)", malformed))
	}
	if updates {
		parseGrokUpdates(&imported, records)
	} else {
		for _, record := range records {
			role := text(record["type"])
			if role == "tool_result" {
				role = "tool"
			}
			if role != "user" && role != "assistant" && role != "tool" {
				continue
			}
			msg, ok := decodeForeignMessage(rawRecord{"role": role, "content": record["content"]}, 0)
			if ok {
				imported.Messages = append(imported.Messages, msg)
			}
		}
	}
	summaryPath := filepath.Join(directory, "summary.json")
	if data, err := os.ReadFile(summaryPath); err == nil {
		var summary rawRecord
		if json.Unmarshal(data, &summary) == nil {
			info := object(summary["info"])
			if id := text(info["id"]); id != "" {
				imported.SourceID = id
			}
			imported.WorkingDir = text(info["cwd"])
			imported.CreatedAt = parseTime(text(summary["created_at"]))
			imported.UpdatedAt = parseTime(text(summary["updated_at"]))
			imported.Title = text(summary["session_summary"])
		}
	}
	setSessionMetadata(&imported)
	if imported.Title == "" {
		imported.Title = firstUserText(imported.Messages)
	}
	return imported, nil
}

func parseGrokUpdates(imported *Session, records []rawRecord) {
	toolCalls := make(map[string]message.ToolCall)
	for _, record := range records {
		params := object(record["params"])
		update := object(params["update"])
		timestamp := int64(number(record["timestamp"]))
		switch text(update["sessionUpdate"]) {
		case "user_message_chunk", "agent_message_chunk":
			content := object(update["content"])
			if content["type"] != "text" {
				continue
			}
			role := message.User
			if update["sessionUpdate"] == "agent_message_chunk" {
				role = message.Assistant
			}
			parts := filterSafeParts([]message.ContentPart{message.TextContent{Text: text(content["text"])}})
			if len(parts) == 0 {
				continue
			}
			model := text(object(update["_meta"])["modelId"])
			imported.Messages = append(imported.Messages, Message{Role: role, Parts: append(parts, finish()), Model: model, Provider: "xai", CreatedAt: timestamp})
		case "tool_call_update":
			id := text(update["toolCallId"])
			status := text(update["status"])
			if _, exists := toolCalls[id]; !exists {
				call := message.ToolCall{ID: id, Name: text(update["kind"]), Input: jsonText(update["rawInput"]), Finished: status != "in_progress"}
				toolCalls[id] = call
				imported.Messages = append(imported.Messages, Message{Role: message.Assistant, Parts: []message.ContentPart{call, message.Finish{Reason: message.FinishReasonToolUse}}, Provider: "xai", CreatedAt: timestamp})
			}
			if status != "completed" && status != "failed" {
				continue
			}
			content := grokToolContent(update["content"])
			if content == "" {
				content = jsonText(update["rawOutput"])
			}
			imported.Messages = append(imported.Messages, Message{Role: message.Tool, Parts: []message.ContentPart{message.ToolResult{ToolCallID: id, Name: toolCalls[id].Name, Content: content, IsError: status == "failed"}, finish()}, Provider: "xai", CreatedAt: timestamp})
		}
	}
}

func grokToolContent(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	var texts []string
	for _, item := range items {
		outer := object(item)
		content := object(outer["content"])
		if content["type"] == "text" && text(content["text"]) != "" {
			texts = append(texts, text(content["text"]))
		}
	}
	return strings.Join(texts, "\n")
}

func parsePi(path string) (Session, error) {
	records, malformed, err := readJSONL(path)
	if err != nil {
		return Session{}, err
	}
	if len(records) == 0 || records[0]["type"] != "session" {
		return Session{}, errors.New("invalid Pi session header")
	}
	header := records[0]
	imported := Session{
		Source:     SourcePi,
		SourceID:   text(header["id"]),
		WorkingDir: text(header["cwd"]),
		CreatedAt:  parseTime(text(header["timestamp"])),
	}
	if malformed > 0 {
		imported.Warnings = append(imported.Warnings, fmt.Sprintf("skipped %d malformed record(s)", malformed))
	}
	byID := make(map[string]rawRecord)
	children := make(map[string]int)
	var leaf rawRecord
	for _, record := range records[1:] {
		id := text(record["id"])
		if id == "" {
			continue
		}
		byID[id] = record
		if parent := text(record["parentId"]); parent != "" {
			children[parent]++
		}
	}
	for id, record := range byID {
		if children[id] == 0 && (leaf == nil || parseTime(text(record["timestamp"])) > parseTime(text(leaf["timestamp"]))) {
			leaf = record
		}
	}
	chain := make([]rawRecord, 0, len(byID))
	seen := make(map[string]bool)
	for leaf != nil {
		id := text(leaf["id"])
		if seen[id] {
			return Session{}, errors.New("Pi session contains a parent cycle")
		}
		seen[id] = true
		chain = append(chain, leaf)
		leaf = byID[text(leaf["parentId"])]
	}
	slices.Reverse(chain)
	model, provider := "", ""
	for _, record := range chain {
		switch record["type"] {
		case "model_change":
			model, provider = text(record["modelId"]), text(record["provider"])
		case "message":
			msg, ok := decodeForeignMessage(record["message"], parseTime(text(record["timestamp"])))
			if ok {
				if msg.Model == "" {
					msg.Model, msg.Provider = model, provider
				}
				imported.Messages = append(imported.Messages, msg)
			}
		case "session_info":
			if imported.Title == "" {
				imported.Title = text(record["name"])
			}
		}
	}
	setSessionMetadata(&imported)
	if imported.Title == "" {
		imported.Title = firstUserText(imported.Messages)
	}
	return imported, nil
}

func appendCodexItem(imported *Session, item rawRecord, timestamp int64) {
	switch text(item["type"]) {
	case "message":
		role := text(item["role"])
		if role != "user" && role != "assistant" {
			return
		}
		parts := decodeBlocks(item["content"])
		parts = filterSafeParts(parts)
		if len(parts) > 0 {
			imported.Messages = append(imported.Messages, Message{Role: message.MessageRole(role), Parts: append(parts, finish()), CreatedAt: timestamp})
		}
	case "function_call", "custom_tool_call", "local_shell_call":
		name := text(item["name"])
		if name == "" && item["type"] == "local_shell_call" {
			name = "local_shell"
		}
		input := item["arguments"]
		if input == nil {
			input = item["input"]
		}
		if input == nil {
			input = item["action"]
		}
		imported.Messages = append(imported.Messages, Message{Role: message.Assistant, CreatedAt: timestamp, Parts: []message.ContentPart{
			message.ToolCall{ID: firstNonEmpty(text(item["call_id"]), text(item["id"])), Name: name, Input: jsonText(input), Finished: true},
			message.Finish{Reason: message.FinishReasonToolUse},
		}})
	case "function_call_output", "custom_tool_call_output":
		imported.Messages = append(imported.Messages, Message{Role: message.Tool, CreatedAt: timestamp, Parts: []message.ContentPart{
			message.ToolResult{ToolCallID: firstNonEmpty(text(item["call_id"]), text(item["id"])), Content: jsonText(item["output"]), IsError: item["success"] == false},
			finish(),
		}})
	}
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

func isGeneratedContext(value string) bool {
	prefixes := []string{
		"<system-reminder>",
		"<system_reminder>",
		"<environment_context>",
		"<user_instructions>",
		"<local-command-caveat>",
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

func claudeTitle(records []rawRecord, messages []Message) string {
	for _, kind := range []string{"custom-title", "ai-title", "summary"} {
		for index := len(records) - 1; index >= 0; index-- {
			if records[index]["type"] == kind {
				for _, key := range []string{"customTitle", "title", "summary"} {
					if value := text(records[index][key]); value != "" {
						return value
					}
				}
			}
		}
	}
	return firstUserText(messages)
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

func codexIDFromPath(path string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".zst"), ".jsonl")
	parts := strings.Split(base, "-")
	if len(parts) >= 8 {
		return strings.Join(parts[len(parts)-5:], "-")
	}
	return base
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
	return value[:limit] + "…"
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
