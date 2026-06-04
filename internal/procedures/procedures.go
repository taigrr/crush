// Package procedures manages stored reusable workflow templates.
// Procedures are stored as markdown files in the user's config
// directory ($XDG_CONFIG_HOME/crush/procedures/).
package procedures

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/taigrr/crush/internal/home"
)

// Procedure represents a stored workflow template.
type Procedure struct {
	// Name is the human-readable name derived from the filename.
	Name string
	// Path is the absolute path to the procedure markdown file.
	Path string
	// Content is the raw markdown content.
	Content string
	// ModifiedAt is the last modification time.
	ModifiedAt time.Time
}

// Dir returns the directory where procedures are stored.
func Dir() string {
	return filepath.Join(home.Config(), "crush", "procedures")
}

// EnsureDir creates the procedures directory if it doesn't exist.
func EnsureDir() error {
	return os.MkdirAll(Dir(), 0o755)
}

// List returns all procedures found in the procedures directory.
func List() ([]Procedure, error) {
	dir := Dir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading procedures directory: %w", err)
	}

	var procedures []Procedure
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		procedures = append(procedures, Procedure{
			Name:       nameFromFilename(entry.Name()),
			Path:       path,
			ModifiedAt: info.ModTime(),
		})
	}
	return procedures, nil
}

// Get reads a procedure by name.
func Get(name string) (Procedure, error) {
	path := filepath.Join(Dir(), filenameFromName(name))
	content, err := os.ReadFile(path)
	if err != nil {
		return Procedure{}, fmt.Errorf("reading procedure %q: %w", name, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return Procedure{}, err
	}
	return Procedure{
		Name:       name,
		Path:       path,
		Content:    string(content),
		ModifiedAt: info.ModTime(),
	}, nil
}

// Save creates or updates a procedure.
func Save(name, content string) error {
	if err := EnsureDir(); err != nil {
		return err
	}
	path := filepath.Join(Dir(), filenameFromName(name))
	return os.WriteFile(path, []byte(content), 0o644)
}

// Delete removes a procedure by name.
func Delete(name string) error {
	path := filepath.Join(Dir(), filenameFromName(name))
	return os.Remove(path)
}

// Exists checks if a procedure exists.
func Exists(name string) bool {
	path := filepath.Join(Dir(), filenameFromName(name))
	_, err := os.Stat(path)
	return err == nil
}

// nameFromFilename converts "my-procedure.md" to "my-procedure".
func nameFromFilename(filename string) string {
	return strings.TrimSuffix(filename, ".md")
}

// filenameFromName converts "my procedure" to "my-procedure.md".
func filenameFromName(name string) string {
	// Replace spaces with hyphens, lowercase, ensure .md suffix.
	sanitized := strings.ToLower(name)
	sanitized = strings.ReplaceAll(sanitized, " ", "-")
	// Remove characters that are problematic in filenames.
	sanitized = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, sanitized)
	if !strings.HasSuffix(sanitized, ".md") {
		sanitized += ".md"
	}
	return sanitized
}
