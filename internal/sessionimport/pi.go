package sessionimport

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

// piHarness imports Pi sessions. Transcripts are single JSONL files
// under ~/.pi/agent/sessions, opened by a "session" header record and
// threaded by id/parentId with model_change markers.
type piHarness struct{}

func (piHarness) id() Source { return SourcePi }

func (piHarness) name() string { return "Pi" }

func (piHarness) root(home string) string {
	return filepath.Join(home, ".pi", "agent", "sessions")
}

func (piHarness) matchWalk(_ string, entry fs.DirEntry) (matched, skipDir bool) {
	return matchJSONLFile(entry), false
}

func (piHarness) detect(_ string, isDir bool, records []rawRecord) bool {
	if isDir {
		return false
	}
	first := records[0]
	return first["type"] == "session" && number(first["version"]) > 0
}

func (piHarness) parse(path string) (Session, error) {
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
	chain, err := latestLeafChain(records[1:], "id", "parentId")
	if err != nil {
		return Session{}, err
	}
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

func (piHarness) discover(path string) (Candidate, error) {
	return discoverMetadata(path, SourcePi, "", func(candidate *Candidate, record rawRecord) {
		if record["type"] == "session" {
			candidate.SourceID = text(record["id"])
			candidate.WorkingDir = text(record["cwd"])
		}
		if candidate.Title == "" && record["type"] == "session_info" {
			candidate.Title = text(record["name"])
		}
		if candidate.Title == "" && record["type"] == "message" {
			candidate.Title = metadataMessageTitle(record["message"])
		}
	})
}
