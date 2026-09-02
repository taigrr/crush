package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSwaggerJSONServed(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/docs/swagger.json", serveSwaggerJSON)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v1/docs/swagger.json")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var spec map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&spec))
	require.Equal(t, "2.0", spec["swagger"])
	require.Equal(t, "/v1", spec["basePath"])
}
