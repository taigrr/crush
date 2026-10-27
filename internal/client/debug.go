package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

// DebugGoroutines returns a full goroutine dump of the targeted server.
func (c *Client) DebugGoroutines(ctx context.Context) (string, error) {
	return c.debugText(ctx, "/debug/goroutines", nil)
}

// DebugLogs returns the last lines of the targeted server's log, filtered
// by minimum level ("" = all) and a case-insensitive substring.
func (c *Client) DebugLogs(ctx context.Context, lines int, level, grep string) (string, error) {
	q := url.Values{}
	if lines > 0 {
		q.Set("lines", strconv.Itoa(lines))
	}
	if level != "" {
		q.Set("level", level)
	}
	if grep != "" {
		q.Set("grep", grep)
	}
	return c.debugText(ctx, "/debug/logs", q)
}

func (c *Client) debugText(ctx context.Context, path string, q url.Values) (string, error) {
	rsp, err := c.get(ctx, path, q, nil)
	if err != nil {
		return "", err
	}
	defer rsp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(rsp.Body, 64<<20))
	if err != nil {
		return "", err
	}
	if rsp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", rsp.Status, data)
	}
	return string(data), nil
}
