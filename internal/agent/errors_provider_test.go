package agent

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/fantasy"
)

func TestProviderErrorDetails(t *testing.T) {
	t.Parallel()

	t.Run("status and message", func(t *testing.T) {
		t.Parallel()
		err := &fantasy.ProviderError{StatusCode: http.StatusBadRequest, Title: "bad request", Message: "prompt is too long"}
		require.Equal(t, "HTTP 400: prompt is too long", providerErrorDetails(err))
		require.Equal(t, "bad request (HTTP 400: prompt is too long)", describeRunError(fmt.Errorf("wrapped: %w", err)))
	})

	t.Run("falls back to body without headers", func(t *testing.T) {
		t.Parallel()
		dump := "HTTP/1.1 400 Bad Request\r\nSet-Cookie: session=secret\r\n\r\n{\"error\":\"context too long\"}"
		err := &fantasy.ProviderError{StatusCode: http.StatusBadRequest, Title: "bad request", ResponseBody: []byte(dump)}
		got := providerErrorDetails(err)
		require.Equal(t, `HTTP 400: {"error":"context too long"}`, got)
		require.NotContains(t, got, "secret")
	})

	t.Run("status only", func(t *testing.T) {
		t.Parallel()
		err := &fantasy.ProviderError{StatusCode: http.StatusBadRequest, Title: "bad request"}
		require.Equal(t, "bad request (HTTP 400)", describeRunError(err))
	})

	t.Run("truncates long body", func(t *testing.T) {
		t.Parallel()
		err := &fantasy.ProviderError{ResponseBody: []byte(strings.Repeat("x", maxProviderErrorBody+50))}
		require.Len(t, providerErrorDetails(err), maxProviderErrorBody+3)
	})

	t.Run("non provider error", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "boom", describeRunError(fmt.Errorf("boom")))
	})
}
