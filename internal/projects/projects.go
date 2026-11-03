package projects

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/taigrr/crush/internal/config"
	"github.com/taigrr/crush/internal/registry"
)

const projectsFileName = "projects.json"

// Project represents a tracked project directory.
type Project struct {
	Path         string    `json:"path"`
	DataDir      string    `json:"data_dir"`
	LastAccessed time.Time `json:"last_accessed"`
}

// ProjectList holds the list of tracked projects.
type ProjectList struct {
	Projects []Project `json:"projects"`
}

var mu sync.Mutex

// projectsFilePath returns the path to the projects.json file.
func projectsFilePath() string {
	return filepath.Join(filepath.Dir(config.GlobalConfigData()), projectsFileName)
}

// Load reads the projects list from disk.
func Load() (*ProjectList, error) {
	mu.Lock()
	defer mu.Unlock()

	path := projectsFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ProjectList{Projects: []Project{}}, nil
		}
		return nil, err
	}

	var list ProjectList
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	return &list, nil
}

// Save writes the projects list to disk.
func Save(list *ProjectList) error {
	mu.Lock()
	defer mu.Unlock()

	path := projectsFilePath()

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0o600)
}

// Register adds or updates a project in the list.
func Register(workingDir, dataDir string) error {
	list, err := Load()
	if err != nil {
		return err
	}

	now := time.Now().UTC()

	// Check if project already exists
	found := false
	for i, p := range list.Projects {
		if p.Path == workingDir {
			list.Projects[i].DataDir = dataDir
			list.Projects[i].LastAccessed = now
			found = true
			break
		}
	}

	if !found {
		list.Projects = append(list.Projects, Project{
			Path:         workingDir,
			DataDir:      dataDir,
			LastAccessed: now,
		})
	}

	// Sort by last accessed (most recent first)
	slices.SortFunc(list.Projects, func(a, b Project) int {
		if a.LastAccessed.After(b.LastAccessed) {
			return -1
		}
		if a.LastAccessed.Before(b.LastAccessed) {
			return 1
		}
		return 0
	})

	return Save(list)
}

// List returns all tracked projects sorted by last accessed.
//
// Workspaces are recorded by the server in the global workspace registry
// ([registry.Store]); projects.json is only written by older clients. Both
// are merged, by path, so commands that enumerate projects see every
// workspace the server has opened.
func List() ([]Project, error) {
	list, err := Load()
	if err != nil {
		return nil, err
	}
	entries, err := registry.New().List()
	if err != nil {
		return nil, err
	}
	out := slices.Clone(list.Projects)
	index := make(map[string]int, len(out))
	for i, p := range out {
		index[p.Path] = i
	}
	for _, e := range entries {
		used := time.Unix(e.LastUsed, 0).UTC()
		if i, ok := index[e.Root]; ok {
			if used.After(out[i].LastAccessed) {
				out[i].LastAccessed = used
				if e.DataDir != "" {
					out[i].DataDir = e.DataDir
				}
			}
			continue
		}
		index[e.Root] = len(out)
		out = append(out, Project{Path: e.Root, DataDir: e.DataDir, LastAccessed: used})
	}
	slices.SortStableFunc(out, func(a, b Project) int {
		return b.LastAccessed.Compare(a.LastAccessed)
	})
	return out, nil
}
