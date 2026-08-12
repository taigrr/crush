package sessionimport

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/klauspost/compress/zstd"
)

type Candidate struct {
	Source     Source `json:"source"`
	Path       string `json:"path"`
	SourceID   string `json:"source_id"`
	Title      string `json:"title"`
	WorkingDir string `json:"working_dir,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	Messages   int    `json:"messages"`
}

type SourceInfo struct {
	Source Source `json:"source"`
	Name   string `json:"name"`
}

func Sources() ([]SourceInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	definitions := sourceDefinitions(home)
	available := make([]SourceInfo, 0, len(definitions))
	for _, definition := range definitions {
		if info, statErr := os.Stat(definition.root); statErr == nil && info.IsDir() {
			available = append(available, SourceInfo{Source: definition.source, Name: definition.name})
		}
	}
	return available, nil
}

func Discover(ctx context.Context, source Source) ([]Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var definition sourceDefinition
	for _, candidate := range sourceDefinitions(home) {
		if candidate.source == source {
			definition = candidate
			break
		}
	}
	if definition.source == "" {
		return nil, errors.New("unsupported session source")
	}

	paths := make([]string, 0)
	err = filepath.WalkDir(definition.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if definition.source == SourceGrok && entry.IsDir() && fileExists(filepath.Join(path, "summary.json")) && (fileExists(filepath.Join(path, "updates.jsonl")) || fileExists(filepath.Join(path, "chat_history.jsonl"))) {
			paths = append(paths, path)
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if definition.source != SourceGrok && (strings.HasSuffix(entry.Name(), ".jsonl") || strings.HasSuffix(entry.Name(), ".jsonl.zst")) {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	workers := min(len(paths), max(1, runtime.GOMAXPROCS(0)*2))
	jobs := make(chan string)
	results := make(chan Candidate, len(paths))
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for path := range jobs {
				candidate, metadataErr := discoverCandidate(path, source)
				if metadataErr == nil && candidate.SourceID != "" {
					results <- candidate
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, path := range paths {
			select {
			case jobs <- path:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wait.Wait()
		close(results)
	}()

	candidates := make([]Candidate, 0, len(paths))
	for candidate := range results {
		candidates = append(candidates, candidate)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(candidates, func(a, b Candidate) int {
		if a.UpdatedAt > b.UpdatedAt {
			return -1
		}
		if a.UpdatedAt < b.UpdatedAt {
			return 1
		}
		return strings.Compare(a.Title, b.Title)
	})
	return candidates, nil
}

func discoverCandidate(path string, source Source) (Candidate, error) {
	if source == SourceGrok {
		return discoverGrokCandidate(path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Candidate{}, err
	}
	candidate := Candidate{Source: source, Path: path, UpdatedAt: info.ModTime().Unix()}
	switch source {
	case SourceClaude:
		candidate.SourceID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	case SourceCodex:
		candidate.SourceID = codexIDFromPath(path)
	case SourcePi:
	default:
		return Candidate{}, errors.New("unsupported session source")
	}

	records, err := readMetadataRecords(path)
	if err != nil {
		return Candidate{}, err
	}
	for _, record := range records {
		timestamp := parseTime(text(record["timestamp"]))
		if timestamp != 0 && (candidate.CreatedAt == 0 || timestamp < candidate.CreatedAt) {
			candidate.CreatedAt = timestamp
		}
		switch source {
		case SourceClaude:
			if candidate.WorkingDir == "" {
				candidate.WorkingDir = text(record["cwd"])
			}
			if candidate.Title == "" && record["type"] == "user" && isClaudeHumanPrompt(record) {
				candidate.Title = metadataMessageTitle(record["message"])
			}
		case SourceCodex:
			payload := object(record["payload"])
			if record["type"] == "session_meta" {
				candidate.SourceID = firstNonEmpty(text(payload["id"]), candidate.SourceID)
				candidate.WorkingDir = text(payload["cwd"])
			}
			if candidate.Title == "" && record["type"] == "response_item" && payload["type"] == "message" && payload["role"] == "user" && isCodexHumanPrompt(payload) {
				candidate.Title = metadataContentTitle(payload["content"])
			}
		case SourcePi:
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
		}
		if candidate.Title != "" && candidate.SourceID != "" && candidate.WorkingDir != "" {
			break
		}
	}
	candidate.Title = metadataTitle(candidate.Title)
	return candidate, nil
}

func discoverGrokCandidate(path string) (Candidate, error) {
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

func readMetadataRecords(path string) ([]rawRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".zst") {
		decoder, decoderErr := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(32<<20))
		if decoderErr != nil {
			return nil, decoderErr
		}
		defer decoder.Close()
		reader = decoder
	}
	const maxMetadataBytes = 1 << 20
	const maxMetadataRecords = 256
	scanner := bufio.NewScanner(io.LimitReader(reader, maxMetadataBytes))
	scanner.Buffer(make([]byte, 64<<10), maxMetadataBytes)
	records := make([]rawRecord, 0, 16)
	for scanner.Scan() && len(records) < maxMetadataRecords {
		var record rawRecord
		if json.Unmarshal(scanner.Bytes(), &record) == nil {
			records = append(records, record)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func metadataMessageTitle(value any) string {
	message := object(value)
	if text(message["role"]) != "user" {
		return ""
	}
	return metadataContentTitle(message["content"])
}

func metadataContentTitle(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	title := strings.TrimSpace(contentTextRaw(data))
	if isGeneratedContent(value) {
		return ""
	}
	return title
}

func metadataTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Untitled session"
	}
	return truncate(strings.Join(strings.Fields(value), " "), 200)
}

type sourceDefinition struct {
	source Source
	name   string
	root   string
}

func sourceDefinitions(home string) []sourceDefinition {
	return []sourceDefinition{
		{source: SourceClaude, name: "Claude Code", root: filepath.Join(home, ".claude", "projects")},
		{source: SourceCodex, name: "Codex", root: filepath.Join(home, ".codex", "sessions")},
		{source: SourceGrok, name: "Grok", root: filepath.Join(home, ".grok", "sessions")},
		{source: SourcePi, name: "Pi", root: filepath.Join(home, ".pi", "agent", "sessions")},
	}
}
