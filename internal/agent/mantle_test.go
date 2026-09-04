package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newJSONResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}

func TestMantleErrorTransport_RewritesStatusAndPreservesBody(t *testing.T) {
	t.Parallel()

	const body = `{"error":{"message":"throttled","code":429}}`
	tr := &mantleErrorTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, body), nil
	})}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example/v1/chat/completions", nil)
	require.NoError(t, err)
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, 429, resp.StatusCode)

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.JSONEq(t, body, string(got))
}

func TestMantleErrorTransport_LeavesSuccessUntouched(t *testing.T) {
	t.Parallel()

	const body = `{"choices":[{"message":{"content":"hi"}}]}`
	tr := &mantleErrorTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return newJSONResponse(http.StatusOK, body), nil
	})}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example/v1/chat/completions", nil)
	require.NoError(t, err)
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.JSONEq(t, body, string(got))
}

func TestMantleErrorTransport_IgnoresNonJSON(t *testing.T) {
	t.Parallel()

	tr := &mantleErrorTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(bytes.NewReader([]byte(`data: {"error":{"message":"x"}}`))),
		}, nil
	})}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://example/v1/chat/completions", nil)
	require.NoError(t, err)
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMantleErrorStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantIsErr  bool
	}{
		{
			name:      "successful completion",
			body:      `{"id":"x","choices":[{"message":{"content":"hi"}}]}`,
			wantIsErr: false,
		},
		{
			name:       "openai error envelope with numeric code",
			body:       `{"error":{"message":"throttled","type":"rate_limit","code":429}}`,
			wantStatus: 429,
			wantIsErr:  true,
		},
		{
			name:       "openai error envelope with string code",
			body:       `{"error":{"message":"bad","type":"auth_error","code":"401"}}`,
			wantStatus: 401,
			wantIsErr:  true,
		},
		{
			name:       "error with non-numeric code falls back to 502",
			body:       `{"error":{"message":"oops","type":"server_error","code":"rate_limit_exceeded"}}`,
			wantStatus: http.StatusBadGateway,
			wantIsErr:  true,
		},
		{
			name:       "error with no code falls back to 502",
			body:       `{"error":{"message":"oops"}}`,
			wantStatus: http.StatusBadGateway,
			wantIsErr:  true,
		},
		{
			name:      "error alongside choices is treated as success",
			body:      `{"error":{"message":"x"},"choices":[{"message":{"content":"hi"}}]}`,
			wantIsErr: false,
		},
		{
			name:      "non-json body",
			body:      `not json`,
			wantIsErr: false,
		},
		{
			name:      "empty body",
			body:      ``,
			wantIsErr: false,
		},
		{
			name:      "sse stream is not json object",
			body:      `data: {"choices":[]}`,
			wantIsErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, isErr := mantleErrorStatus([]byte(tt.body))
			require.Equal(t, tt.wantIsErr, isErr)
			if tt.wantIsErr {
				require.Equal(t, tt.wantStatus, status)
			}
		})
	}
}
