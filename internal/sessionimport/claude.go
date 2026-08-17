package sessionimport

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
)

// claudeHarness imports Claude Code sessions. Transcripts are single
// JSONL files under ~/.claude/projects, threaded by uuid/parentUuid
// with optional sidechains that are ignored.
type claudeHarness struct{}

func (claudeHarness) id() Source { return SourceClaude }

func (claudeHarness) name() string { return "Claude Code" }

func (claudeHarness) root(home string) string {
	return filepath.Join(home, ".claude", "projects")
}

func (claudeHarness) matchWalk(_ string, entry fs.DirEntry) (matched, skipDir bool) {
	return matchJSONLFile(entry), false
}

func (claudeHarness) detect(_ string, isDir bool, records []rawRecord) bool {
	if isDir {
		return false
	}
	first := records[0]
	if _, ok := first["sessionId"]; ok {
		return true
	}
	_, ok := first["uuid"]
	return ok
}

func (claudeHarness) parse(path string) (Session, error) {
	records, malformed, err := readJSONL(path)
	if err != nil {
		return Session{}, err
	}
	filtered := make([]rawRecord, 0, len(records))
	for _, record := range records {
		if boolValue(record["isSidechain"]) {
			continue
		}
		kind := text(record["type"])
		if kind != "user" && kind != "assistant" {
			continue
		}
		filtered = append(filtered, record)
	}
	chain, err := latestLeafChain(filtered, "uuid", "parentUuid")
	if err != nil {
		return Session{}, err
	}
	imported := Session{Source: SourceClaude, SourceID: jsonlSourceID(path)}
	if malformed > 0 {
		imported.Warnings = append(imported.Warnings, fmt.Sprintf("skipped %d malformed record(s)", malformed))
	}
	for _, record := range chain {
		if imported.WorkingDir == "" {
			imported.WorkingDir = text(record["cwd"])
		}
		if record["type"] == "user" && !isClaudeHumanPrompt(record) {
			continue
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

func (claudeHarness) discover(path string) (Candidate, error) {
	return discoverMetadata(path, SourceClaude, jsonlSourceID(path), func(candidate *Candidate, record rawRecord) {
		if candidate.WorkingDir == "" {
			candidate.WorkingDir = text(record["cwd"])
		}
		if candidate.Title == "" && record["type"] == "user" && isClaudeHumanPrompt(record) {
			candidate.Title = metadataMessageTitle(record["message"])
		}
	})
}

func isClaudeHumanPrompt(record rawRecord) bool {
	if boolValue(record["isMeta"]) {
		return false
	}
	origin := object(record["origin"])
	originKind := text(origin["kind"])
	promptSource := text(record["promptSource"])
	if originKind != "" || promptSource != "" {
		return originKind == "human" || promptSource == "typed"
	}
	return !isGeneratedMessage(record["message"])
}

func claudeTitle(records []rawRecord, messages []Message) string {
	for _, kind := range []string{"custom-title", "ai-title", "summary"} {
		for _, record := range slices.Backward(records) {
			if record["type"] == kind {
				for _, key := range []string{"customTitle", "title", "summary"} {
					if value := text(record[key]); value != "" {
						return value
					}
				}
			}
		}
	}
	return firstUserText(messages)
}
