package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDebugGoroutines(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	handleDebugGoroutines(rec, httptest.NewRequest(http.MethodGet, "/v1/debug/goroutines", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "TestDebugGoroutines")
}

func TestTailLogs(t *testing.T) {
	t.Parallel()
	var src strings.Builder
	for i := range 10 {
		level := "INFO"
		if i%3 == 0 {
			level = "WARN"
		}
		src.WriteString(`{"level":"` + level + `","msg":"m` + string(rune('0'+i)) + `"}` + "\n")
	}
	lines, err := tailLogs(strings.NewReader(src.String()), 2, "", "warn")
	require.NoError(t, err)
	require.Equal(t, []string{`{"level":"WARN","msg":"m6"}`, `{"level":"WARN","msg":"m9"}`}, lines)
	lines, err = tailLogs(strings.NewReader(src.String()), 3, "m1", "")
	require.NoError(t, err)
	require.Equal(t, []string{`{"level":"INFO","msg":"m1"}`}, lines)
}
