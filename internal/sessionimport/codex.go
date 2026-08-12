package sessionimport

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/taigrr/crush/internal/message"
)

// codexHarness imports OpenAI Codex sessions. Transcripts are JSONL
// rollout files under ~/.codex/sessions, with response items, tool
// calls, compaction snapshots, and thread-rollback events.
type codexHarness struct{}

func (codexHarness) id() Source { return SourceCodex }

func (codexHarness) name() string { return "Codex" }

func (codexHarness) root(home string) string {
	return filepath.Join(home, ".codex", "sessions")
}

func (codexHarness) matchWalk(_ string, entry fs.DirEntry) (matched, skipDir bool) {
	return matchJSONLFile(entry), false
}

func (codexHarness) detect(_ string, isDir bool, first rawRecord) bool {
	if isDir {
		return false
	}
	return first["type"] == "session_meta" || first["type"] == "turn_context"
}

func (codexHarness) parse(path string) (Session, error) {
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
			if payload["type"] == "message" && payload["role"] == "user" && !isCodexHumanPrompt(payload) {
				continue
			}
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

func (codexHarness) discover(path string) (Candidate, error) {
	return discoverMetadata(path, SourceCodex, codexIDFromPath(path), func(candidate *Candidate, record rawRecord) {
		payload := object(record["payload"])
		if record["type"] == "session_meta" {
			candidate.SourceID = firstNonEmpty(text(payload["id"]), candidate.SourceID)
			candidate.WorkingDir = text(payload["cwd"])
		}
		if candidate.Title == "" && record["type"] == "response_item" && payload["type"] == "message" && payload["role"] == "user" && isCodexHumanPrompt(payload) {
			candidate.Title = metadataContentTitle(payload["content"])
		}
	})
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

func isCodexHumanPrompt(payload rawRecord) bool {
	metadata := object(payload["internal_chat_message_metadata_passthrough"])
	if text(metadata["turn_id"]) != "" {
		return true
	}
	return !isGeneratedContent(payload["content"])
}

func codexIDFromPath(path string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".zst"), ".jsonl")
	parts := strings.Split(base, "-")
	if len(parts) >= 8 {
		return strings.Join(parts[len(parts)-5:], "-")
	}
	return base
}
