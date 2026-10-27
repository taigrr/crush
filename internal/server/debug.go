package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"

	crushlog "github.com/taigrr/crush/internal/log"
)

const (
	defaultDebugLogLines = 200
	maxDebugLogLines     = 5000
)

// installDebugRoutes exposes introspection for diagnosing a stuck server.
// It is reachable over the local socket and, through the node proxy, from
// paired cluster members only.
func (s *Server) installDebugRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/debug/goroutines", handleDebugGoroutines)
	mux.HandleFunc("GET /v1/debug/logs", handleDebugLogs)
}

func handleDebugGoroutines(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_ = pprof.Lookup("goroutine").WriteTo(w, 2)
}

// handleDebugLogs returns the last lines of the server log, optionally
// filtered by minimum level and a case-insensitive substring.
func handleDebugLogs(w http.ResponseWriter, r *http.Request) {
	path := crushlog.File()
	if path == "" {
		http.Error(w, "this server has no log file", http.StatusNotFound)
		return
	}
	q := r.URL.Query()
	lines := defaultDebugLogLines
	if n, err := strconv.Atoi(q.Get("lines")); err == nil && n > 0 {
		lines = min(n, maxDebugLogLines)
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	out, err := tailLogs(f, lines, q.Get("grep"), q.Get("level"))
	if err != nil && out == nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	for _, line := range out {
		_, _ = io.WriteString(w, line+"\n")
	}
}

// tailLogs keeps the last n JSON log lines of r that contain grep (case
// insensitive) and are at least level. A read error after some lines is
// returned alongside them.
func tailLogs(r io.Reader, n int, grep, level string) ([]string, error) {
	grep = strings.ToLower(grep)
	var minLevel slog.Level
	hasLevel := level != ""
	if hasLevel {
		if err := minLevel.UnmarshalText([]byte(level)); err != nil {
			return nil, err
		}
	}
	ring := make([]string, 0, n)
	next := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if grep != "" && !strings.Contains(strings.ToLower(line), grep) {
			continue
		}
		if hasLevel {
			var rec struct {
				Level slog.Level `json:"level"`
			}
			if json.Unmarshal([]byte(line), &rec) != nil || rec.Level < minLevel {
				continue
			}
		}
		if len(ring) < n {
			ring = append(ring, line)
		} else {
			ring[next] = line
		}
		next = (next + 1) % n
	}
	if len(ring) == n {
		ring = slices.Concat(ring[next:], ring[:next])
	}
	return ring, sc.Err()
}
