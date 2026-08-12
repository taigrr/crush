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
	available := make([]SourceInfo, 0, len(registry))
	for _, h := range registry {
		if info, statErr := os.Stat(h.root(home)); statErr == nil && info.IsDir() {
			available = append(available, SourceInfo{Source: h.id(), Name: h.name()})
		}
	}
	slices.SortFunc(available, func(a, b SourceInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
	return available, nil
}

func Discover(ctx context.Context, source Source) ([]Candidate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	h := harnessByID(source)
	if h == nil {
		return nil, errors.New("unsupported session source")
	}

	paths, err := collectSessionPaths(ctx, h, h.root(home))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	candidates := discoverCandidates(ctx, h, paths)
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

// discoverCandidates reads metadata for paths concurrently, keeping
// only entries that resolve a source ID.
func discoverCandidates(ctx context.Context, h harness, paths []string) []Candidate {
	workers := min(len(paths), max(1, runtime.GOMAXPROCS(0)*2))
	jobs := make(chan string)
	results := make(chan Candidate, len(paths))
	var wait sync.WaitGroup
	wait.Add(workers)
	for range workers {
		go func() {
			defer wait.Done()
			for path := range jobs {
				candidate, metadataErr := h.discover(path)
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
	return candidates
}

func collectSessionPaths(ctx context.Context, h harness, root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		matched, skipDir := h.matchWalk(path, entry)
		if matched {
			paths = append(paths, path)
		}
		if skipDir {
			return fs.SkipDir
		}
		return nil
	})
	return paths, err
}

// discoverMetadata reads the leading metadata records of a JSONL
// transcript into a [Candidate], applying a harness-specific per-record
// hook. It is the shared cheap-scan path used by file-based harnesses.
func discoverMetadata(path string, source Source, sourceID string, apply func(*Candidate, rawRecord)) (Candidate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Candidate{}, err
	}
	candidate := Candidate{Source: source, Path: path, SourceID: sourceID, UpdatedAt: info.ModTime().Unix()}
	records, err := readMetadataRecords(path)
	if err != nil {
		return Candidate{}, err
	}
	for _, record := range records {
		timestamp := parseTime(text(record["timestamp"]))
		if timestamp != 0 && (candidate.CreatedAt == 0 || timestamp < candidate.CreatedAt) {
			candidate.CreatedAt = timestamp
		}
		apply(&candidate, record)
		if candidate.Title != "" && candidate.SourceID != "" && candidate.WorkingDir != "" {
			break
		}
	}
	candidate.Title = metadataTitle(candidate.Title)
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
