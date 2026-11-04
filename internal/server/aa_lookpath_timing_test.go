package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAALookPathTiming(t *testing.T) {
	path := os.Getenv("PATH")
	start := time.Now()
	var wg sync.WaitGroup
	for i := range 131 {
		wg.Go(func() { _, _ = exec.LookPath(fmt.Sprintf("no-such-lsp-%d", i)) })
	}
	wg.Wait()
	t.Errorf("DEBUG aa: 131 parallel LookPath took %s; PATH entries=%d PATHEXT=%q",
		time.Since(start), len(filepath.SplitList(path)), os.Getenv("PATHEXT"))
	_ = strings.TrimSpace
}
