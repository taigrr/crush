package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceOverviews_RejectsBadLimit(t *testing.T) {
	t.Parallel()
	c := &controllerV1{}
	for _, v := range []string{"x", "-1"} {
		rec := httptest.NewRecorder()
		c.handleGetWorkspaceOverviews(rec, httptest.NewRequest(http.MethodGet, "/v1/workspace-overviews?limit="+v, nil))
		require.Equal(t, http.StatusBadRequest, rec.Code, v)
	}
}
