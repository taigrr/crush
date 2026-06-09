package agent

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// mantleErrorTransport detects Bedrock Mantle (OpenAI-compatible) error
// responses that are returned with an HTTP 200 status and rewrites them to
// a real error status so the provider SDK surfaces the failure instead of
// silently parsing a body that contains no completion.
//
// Bedrock's mantle endpoint returns OpenAI-style `{"error": {...}}`
// envelopes with `200 OK` rather than the expected 4xx/5xx, which leaves
// Crush in a wedged state (no assistant content, no error). This transport
// only inspects non-streaming JSON bodies; streaming (SSE) responses are
// left untouched and their in-stream errors are handled by the provider.
type mantleErrorTransport struct {
	base http.RoundTripper
}

func (t *mantleErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return resp, nil
	}

	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		// Surface the read failure as a body the caller can still drain.
		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, nil
	}
	// Always restore the body so the caller sees the original payload.
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))

	if status, ok := mantleErrorStatus(body); ok {
		resp.StatusCode = status
		resp.Status = strconv.Itoa(status) + " " + http.StatusText(status)
	}
	return resp, nil
}

// mantleErrorStatus reports whether body is a Bedrock Mantle error envelope
// (returned with HTTP 200) and, if so, the HTTP status that should be
// surfaced in its place. A successful chat completion carries a top-level
// "choices" array; an error carries a top-level "error" object in the
// OpenAI error shape. The embedded numeric code is preferred when it is a
// plausible HTTP status; otherwise 502 Bad Gateway is used to signal an
// upstream failure.
func mantleErrorStatus(body []byte) (int, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return 0, false
	}

	var probe struct {
		Error *struct {
			Message string          `json:"message"`
			Type    string          `json:"type"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
		Choices []json.RawMessage `json:"choices"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return 0, false
	}
	// An error envelope must carry an error object and must not also carry a
	// successful completion.
	if probe.Error == nil || len(probe.Choices) > 0 {
		return 0, false
	}

	if code := parseErrorCode(probe.Error.Code); code >= 400 && code <= 599 {
		return code, true
	}
	return http.StatusBadGateway, true
}

// parseErrorCode extracts an HTTP status code from the OpenAI-style error
// "code" field, which may be a JSON number or a numeric string. Non-numeric
// codes (e.g. "rate_limit_exceeded") yield 0.
func parseErrorCode(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var num int
	if err := json.Unmarshal(raw, &num); err == nil {
		return num
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		if n, err := strconv.Atoi(str); err == nil {
			return n
		}
	}
	return 0
}
