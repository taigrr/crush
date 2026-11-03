package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/proto"
)

func sampleOverviews(n int) []proto.WorkspaceOverview {
	ov := proto.WorkspaceOverview{Root: "/Users/me/code/project", DataDir: "/Users/me/code/project/.crush", WorkspaceID: "ws-1", Attached: true}
	for i := range n {
		ov.Sessions = append(ov.Sessions, proto.SessionOverview{
			ID:         fmt.Sprintf("0f8b1c4e-1111-4222-8333-%012d", i),
			Title:      fmt.Sprintf("Fix the overview replica labeling bug number %d", i),
			WorkingDir: "/Users/me/code/project/internal/server",
			UpdatedAt:  1_700_000_000_000 + int64(i)*1000,
			Color:      "aliceblue",
			Animal:     "tiger",
		})
	}
	return []proto.WorkspaceOverview{ov}
}

func TestJSONEncodeGzip_RoundTrip(t *testing.T) {
	t.Parallel()
	want := sampleOverviews(1500)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jsonEncodeGzip(w, r, want)
	}))
	t.Cleanup(srv.Close)

	// Go's transport requests gzip itself and decodes it transparently,
	// which is what the cluster CallAPI relies on.
	rsp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer rsp.Body.Close()
	require.True(t, rsp.Uncompressed)
	var got []proto.WorkspaceOverview
	require.NoError(t, json.NewDecoder(rsp.Body).Decode(&got))
	require.Equal(t, want, got)

	// Measure the wire size with and without compression.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept-Encoding", "gzip")
	raw, err := http.DefaultTransport.RoundTrip(req)
	require.NoError(t, err)
	defer raw.Body.Close()
	require.Equal(t, "gzip", raw.Header.Get("Content-Encoding"))
	zipped, _ := io.ReadAll(raw.Body)
	var plain bytes.Buffer
	require.NoError(t, json.NewEncoder(&plain).Encode(want))
	t.Logf("1500 sessions: %d bytes JSON, %d bytes gzip (%.1f%%)", plain.Len(), len(zipped), 100*float64(len(zipped))/float64(plain.Len()))
	require.Less(t, len(zipped), plain.Len()/4)
}

func TestJSONEncodeGzip_PlainWithoutAcceptEncoding(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	jsonEncodeGzip(rec, httptest.NewRequest(http.MethodGet, "/", nil), []int{1})
	require.Empty(t, rec.Header().Get("Content-Encoding"))
	require.JSONEq(t, "[1]", rec.Body.String())
}

func TestAcceptsGzip(t *testing.T) {
	t.Parallel()
	for header, want := range map[string]bool{
		"":                  false,
		"gzip":              true,
		"br, GZIP":          true,
		"gzip;q=0":          false,
		"gzip; q=0.5":       true,
		"deflate, identity": false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", header)
		require.Equal(t, want, acceptsGzip(r), header)
	}
}
