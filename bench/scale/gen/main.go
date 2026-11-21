// Command gen builds a deterministic scale fixture: N workspaces, each with
// its own .crush/crush.db (crush's real migrations) holding M sessions, all
// registered in the global workspace registry under an isolated root.
//
// Usage: go run ./bench/scale/gen -root DIR -workspaces N -sessions M
//
// DIR gets the same layout bench/startup.py uses (home, config, data,
// cache, state, work). Point HOME/XDG_*/CRUSH_GLOBAL_* at it before
// starting crush against the fixture.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/taigrr/crush/bench/scale"
	"github.com/taigrr/crush/internal/db"
	"github.com/taigrr/crush/internal/registry"
)

func main() {
	root := flag.String("root", "", "fixture root (required)")
	nWS := flag.Int("workspaces", 10, "number of workspaces")
	nSess := flag.Int("sessions", 200, "sessions per workspace")
	msgs := flag.Int("msgs", 4, "messages per regular session")
	longN := flag.Int("long", 3, "long-history sessions per workspace")
	longMsgs := flag.Int("long-msgs", 300, "messages per long session")
	par := flag.Int("par", 4, "parallel workspace writers")
	clone := flag.Bool("clone", false, "fill one database and copy it to every workspace (session IDs repeat across workspaces; much faster)")
	flag.Parse()
	if *root == "" {
		log.Fatal("-root is required")
	}
	for _, d := range []string{"home", "config/crush", "data/crush", "cache", "state", "work"} {
		must(os.MkdirAll(filepath.Join(*root, d), 0o755))
	}
	start := time.Now()
	tmpl := filepath.Join(*root, "template")
	conn, err := db.Connect(context.Background(), tmpl)
	must(err)
	_, err = conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	must(err)
	must(db.Release(tmpl))
	tmplDB := filepath.Join(tmpl, "crush.db")

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	reg := registry.NewWithPath(filepath.Join(*root, "data", "crush", "workspaces.jsonl"))
	if *clone {
		must(scale.Fill(tmplDB, 0, *nSess, *msgs, *longN, *longMsgs, base))
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range *par {
		wg.Go(func() {
			for i := range jobs {
				ws := filepath.Join(*root, "work", fmt.Sprintf("ws%04d", i))
				dd := filepath.Join(ws, ".crush")
				must(os.MkdirAll(dd, 0o755))
				must(scale.CopyFile(tmplDB, filepath.Join(dd, "crush.db")))
				if !*clone {
					must(scale.Fill(filepath.Join(dd, "crush.db"), i, *nSess, *msgs, *longN, *longMsgs, base))
				}
			}
		})
	}
	for i := range *nWS {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	// Register in index order with increasing recency so ws0000 is the
	// most recently used, matching how the picker orders them.
	for i := *nWS - 1; i >= 0; i-- {
		ws := filepath.Join(*root, "work", fmt.Sprintf("ws%04d", i))
		must(reg.Add(registry.Entry{Root: ws, DataDir: filepath.Join(ws, ".crush"), LastUsed: base + int64(*nWS-i)}))
	}
	must(os.RemoveAll(tmpl))
	cfg, _ := json.Marshal(map[string]any{
		"providers": map[string]any{"fake": map[string]any{
			"type": "openai-compat", "base_url": "http://127.0.0.1:9/v1", "api_key": "bench",
			"models": []any{map[string]any{"id": "m", "name": "m", "context_window": 8000, "default_max_tokens": 1000}},
		}},
		"models": map[string]any{
			"large": map[string]any{"model": "m", "provider": "fake"},
			"small": map[string]any{"model": "m", "provider": "fake"},
		},
	})
	must(os.WriteFile(filepath.Join(*root, "config", "crush", "crush.json"), cfg, 0o644))
	fmt.Printf("generated %d workspaces x %d sessions in %s\n", *nWS, *nSess, time.Since(start).Round(time.Millisecond))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
