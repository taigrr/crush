package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taigrr/crush/internal/proto"
)

func TestSearchHistoryEncodesRequestAndDecodesResult(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod string
	var gotParams proto.SearchHistoryParams
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotParams)
		_ = json.NewEncoder(w).Encode(proto.SearchHistoryResult{
			Total:        1,
			SemanticUsed: true,
			Hits: []proto.SessionHit{
				{SessionID: "s1", SessionTitle: "One", WorkspaceID: "ws1", Score: 0.9, Match: "both", Snippet: "hi"},
			},
		})
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	on := true
	res, err := c.SearchHistory(context.Background(), "ws1", proto.SearchHistoryParams{
		Query:    "hello",
		Semantic: &on,
	})
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/v1/workspaces/ws1/history/search", gotPath)
	require.Equal(t, "hello", gotParams.Query)
	require.NotNil(t, gotParams.Semantic)
	require.True(t, *gotParams.Semantic)

	require.Equal(t, 1, res.Total)
	require.True(t, res.SemanticUsed)
	require.Len(t, res.Hits, 1)
	require.Equal(t, "s1", res.Hits[0].SessionID)
	require.Equal(t, "ws1", res.Hits[0].WorkspaceID)
}

func TestSearchHistoryReturnsErrorOnNon200(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := captureClient(t, srv)
	_, err := c.SearchHistory(context.Background(), "ws1", proto.SearchHistoryParams{Query: "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "status code 500")
}
