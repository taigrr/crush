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

func (grokHarness) detect(path string, isDir bool, first rawRecord) bool {
	if isDir {
		return isGrokSessionDir(path)
	}
	return first["type"] == "system" || first["type"] == "user" || first["type"] == "assistant"
}

func (grokHarness) parse(path string) (Session, error) {
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
