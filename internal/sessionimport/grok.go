package sessionimport

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/taigrr/crush/internal/message"
)

// grokHarness imports Grok Build sessions. Each session is a directory
// under ~/.grok/sessions containing summary.json plus either
// updates.jsonl (ACP-style streaming updates) or chat_history.jsonl.
type grokHarness struct{}

func (grokHarness) id() Source { return SourceGrok }

func (grokHarness) name() string { return "Grok" }

func (grokHarness) root(home string) string {
	return filepath.Join(home, ".grok", "sessions")
}

func (grokHarness) matchWalk(path string, entry fs.DirEntry) (matched, skipDir bool) {
	if entry.IsDir() && isGrokSessionDir(path) {
		return true, true
	}
	return false, false
}

func (grokHarness) detect(path string, isDir bool, records []rawRecord) bool {
	if isDir {
		return isGrokSessionDir(path)
	}
	first := records[0]
	if filepath.Base(path) == "updates.jsonl" || grokUpdate(first)["sessionUpdate"] != nil {
		return true
	}
	if first["type"] == "system" || first["type"] == "user" || first["type"] == "assistant" {
		return true
	}
	return hasGrokSessionUpdate(records)
}

func (grokHarness) parse(path string) (Session, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Session{}, err
	}
	directory := filepath.Dir(path)
	if info.IsDir() {
		directory = path
		if fileExists(filepath.Join(path, "updates.jsonl")) {
			path = filepath.Join(path, "updates.jsonl")
		} else {
			path = filepath.Join(path, "chat_history.jsonl")
		}
	}
	// Derive SourceID from the session directory so importing the
	// directory and importing a transcript file inside it yield the same
	// deterministic session ID. applyGrokSummary overrides this with the
	// summary's info.id when present.
	sourceID := filepath.Base(directory)
	records, malformed, err := readJSONL(path)
	if err != nil {
		return Session{}, err
	}
	imported := Session{Source: SourceGrok, SourceID: sourceID}
	if malformed > 0 {
		imported.Warnings = append(imported.Warnings, fmt.Sprintf("skipped %d malformed record(s)", malformed))
	}
	if filepath.Base(path) == "updates.jsonl" || hasGrokSessionUpdate(records) {
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
	applyGrokSummary(&imported, directory)
	setSessionMetadata(&imported)
	if imported.Title == "" {
		imported.Title = firstUserText(imported.Messages)
	}
	return imported, nil
}

func (grokHarness) discover(path string) (Candidate, error) {
	data, err := os.ReadFile(filepath.Join(path, "summary.json"))
	if err != nil {
		return Candidate{}, err
	}
	var summary rawRecord
	if err := json.Unmarshal(data, &summary); err != nil {
		return Candidate{}, err
	}
	info := object(summary["info"])
	candidate := Candidate{
		Source:     SourceGrok,
		Path:       path,
		SourceID:   firstNonEmpty(text(info["id"]), filepath.Base(path)),
		Title:      metadataTitle(text(summary["session_summary"])),
		WorkingDir: text(info["cwd"]),
		CreatedAt:  parseTime(text(summary["created_at"])),
		UpdatedAt:  parseTime(text(summary["updated_at"])),
	}
	if candidate.UpdatedAt == 0 {
		stat, statErr := os.Stat(path)
		if statErr != nil {
			return Candidate{}, statErr
		}
		candidate.UpdatedAt = stat.ModTime().Unix()
	}
	return candidate, nil
}

// isGrokSessionDir reports whether dir is a Grok session directory:
// summary.json plus a transcript file.
func isGrokSessionDir(dir string) bool {
	return fileExists(filepath.Join(dir, "summary.json")) &&
		(fileExists(filepath.Join(dir, "updates.jsonl")) || fileExists(filepath.Join(dir, "chat_history.jsonl")))
}

func applyGrokSummary(imported *Session, directory string) {
	data, err := os.ReadFile(filepath.Join(directory, "summary.json"))
	if err != nil {
		return
	}
	var summary rawRecord
	if json.Unmarshal(data, &summary) != nil {
		return
	}
	info := object(summary["info"])
	if id := text(info["id"]); id != "" {
		imported.SourceID = id
	}
	imported.WorkingDir = text(info["cwd"])
	imported.CreatedAt = parseTime(text(summary["created_at"]))
	imported.UpdatedAt = parseTime(text(summary["updated_at"]))
	imported.Title = text(summary["session_summary"])
}

func parseGrokUpdates(imported *Session, records []rawRecord) {
	toolCalls := make(map[string]message.ToolCall)
	results := make(map[string]int)
	var pending *Message
	flush := func() {
		if pending == nil {
			return
		}
		pending.Parts = filterSafeParts(pending.Parts)
		if len(pending.Parts) == 0 {
			pending = nil
			return
		}
		pending.Parts = append(pending.Parts, finish())
		imported.Messages = append(imported.Messages, *pending)
		pending = nil
	}
	for _, record := range records {
		update := grokUpdate(record)
		timestamp := parseTimestamp(firstNonNil(record["timestamp"], update["timestamp"]))
		switch text(update["sessionUpdate"]) {
		case "user_message_chunk", "agent_message_chunk":
			content := object(update["content"])
			if content["type"] != "text" {
				continue
			}
			chunk := text(content["text"])
			if chunk == "" {
				continue
			}
			role := message.User
			if update["sessionUpdate"] == "agent_message_chunk" {
				role = message.Assistant
			}
			model := text(object(update["_meta"])["modelId"])
			if pending != nil && pending.Role == role {
				appendGrokChunk(pending, chunk)
				if pending.Model == "" {
					pending.Model = model
				}
				continue
			}
			flush()
			pending = &Message{
				Role:      role,
				Parts:     []message.ContentPart{message.TextContent{Text: chunk}},
				Model:     model,
				Provider:  "xai",
				CreatedAt: timestamp,
			}
		case "tool_call":
			flush()
			appendGrokToolCall(imported, toolCalls, update, timestamp)
			appendGrokToolResult(imported, toolCalls, results, update, timestamp)
		case "tool_call_update":
			flush()
			id := firstNonEmpty(text(update["toolCallId"]), text(update["id"]))
			if _, exists := toolCalls[id]; !exists {
				appendGrokToolCall(imported, toolCalls, update, timestamp)
			} else {
				mergeGrokToolCall(imported, toolCalls, update)
			}
			appendGrokToolResult(imported, toolCalls, results, update, timestamp)
		}
	}
	flush()
}

// grokUpdate unwraps an ACP session/update record (or a bare update
// object) into the inner sessionUpdate payload.
func grokUpdate(record rawRecord) rawRecord {
	if update := object(object(record["params"])["update"]); update["sessionUpdate"] != nil {
		return update
	}
	if update := object(record["update"]); update["sessionUpdate"] != nil {
		return update
	}
	return record
}

func appendGrokChunk(pending *Message, chunk string) {
	if len(pending.Parts) > 0 {
		if existing, ok := pending.Parts[len(pending.Parts)-1].(message.TextContent); ok {
			pending.Parts[len(pending.Parts)-1] = message.TextContent{Text: existing.Text + chunk}
			return
		}
	}
	pending.Parts = append(pending.Parts, message.TextContent{Text: chunk})
}

func grokToolFinished(status string) bool {
	return status == "completed" || status == "failed"
}

func grokToolFields(update rawRecord) (id, name, title, kind, input string) {
	id = firstNonEmpty(text(update["toolCallId"]), text(update["id"]))
	meta := object(object(update["_meta"])["x.ai/tool"])
	name = firstNonEmpty(text(update["name"]), text(update["tool"]), text(meta["name"]))
	title = text(update["title"])
	kind = text(update["kind"])
	input = firstNonEmpty(jsonText(update["rawInput"]), jsonText(update["arguments"]), jsonText(update["input"]))
	return id, name, title, kind, input
}

func hasGrokSessionUpdate(records []rawRecord) bool {
	for _, record := range records {
		if grokUpdate(record)["sessionUpdate"] != nil {
			return true
		}
	}
	return false
}

func appendGrokToolCall(imported *Session, toolCalls map[string]message.ToolCall, update rawRecord, timestamp int64) {
	id, name, title, kind, input := grokToolFields(update)
	if name == "" {
		name = firstNonEmpty(title, kind)
	}
	call := message.ToolCall{ID: id, Name: name, Input: input, Finished: grokToolFinished(text(update["status"]))}
	if id != "" {
		toolCalls[id] = call
	}
	imported.Messages = append(imported.Messages, Message{
		Role:      message.Assistant,
		Parts:     []message.ContentPart{call, message.Finish{Reason: message.FinishReasonToolUse}},
		Provider:  "xai",
		CreatedAt: timestamp,
	})
}

func mergeGrokToolCall(imported *Session, toolCalls map[string]message.ToolCall, update rawRecord) {
	id, name, title, kind, input := grokToolFields(update)
	call := toolCalls[id]
	if name != "" {
		call.Name = name
	} else if call.Name == "" {
		call.Name = firstNonEmpty(title, kind)
	}
	if input != "" {
		call.Input = input
	}
	if grokToolFinished(text(update["status"])) {
		call.Finished = true
	}
	toolCalls[id] = call
	replaceGrokToolCall(imported, call)
}

func appendGrokToolResult(imported *Session, toolCalls map[string]message.ToolCall, results map[string]int, update rawRecord, timestamp int64) {
	status := text(update["status"])
	if !grokToolFinished(status) {
		return
	}
	id := firstNonEmpty(text(update["toolCallId"]), text(update["id"]))
	finishGrokToolCall(imported, toolCalls, id)
	content := grokToolContent(update["content"])
	if content == "" {
		content = jsonText(update["rawOutput"])
	}
	result := message.ToolResult{ToolCallID: id, Name: toolCalls[id].Name, Content: content, IsError: status == "failed"}
	// Only dedup keyed results. Without an id, distinct tool completions
	// would all collide on the empty-string key and overwrite each other,
	// so append each id-less result as its own message instead.
	if id != "" {
		if index, exists := results[id]; exists && index < len(imported.Messages) {
			imported.Messages[index].Parts = []message.ContentPart{result, finish()}
			imported.Messages[index].CreatedAt = timestamp
			return
		}
		results[id] = len(imported.Messages)
	}
	imported.Messages = append(imported.Messages, Message{
		Role:      message.Tool,
		Parts:     []message.ContentPart{result, finish()},
		Provider:  "xai",
		CreatedAt: timestamp,
	})
}

func finishGrokToolCall(imported *Session, toolCalls map[string]message.ToolCall, id string) {
	// Never track id-less calls: a zero-value entry under the empty key
	// would make a later id-less tool_call_update look like an existing
	// call and route to a merge that emits a result with no tool call.
	if id == "" {
		return
	}
	call := toolCalls[id]
	call.Finished = true
	toolCalls[id] = call
	replaceGrokToolCall(imported, call)
}

func replaceGrokToolCall(imported *Session, call message.ToolCall) {
	if call.ID == "" {
		return
	}
	for index := range imported.Messages {
		for partIndex, part := range imported.Messages[index].Parts {
			existing, ok := part.(message.ToolCall)
			if !ok || existing.ID != call.ID {
				continue
			}
			imported.Messages[index].Parts[partIndex] = call
			return
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
