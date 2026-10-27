package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/taigrr/crush/internal/cluster"
	"github.com/taigrr/crush/internal/proto"
)

// clusterCall performs a cluster control RPC, surfacing the server's
// error message on failure.
func clusterCall[T any](ctx context.Context, c *Client, method, path string, in any) (T, error) {
	var out T
	var body io.Reader
	var headers http.Header
	if in != nil {
		body = jsonBody(in)
		headers = jsonHeader()
	}
	rsp, err := c.sendReq(ctx, method, path, nil, body, headers)
	if err != nil {
		return out, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		var pe proto.Error
		_ = json.NewDecoder(rsp.Body).Decode(&pe)
		if pe.Message != "" {
			return out, fmt.Errorf("%s", pe.Message)
		}
		return out, fmt.Errorf("cluster request failed: %s", rsp.Status)
	}
	if err := json.NewDecoder(rsp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("decode cluster response: %w", err)
	}
	return out, nil
}

// ClusterStatus returns the server's cluster membership.
func (c *Client) ClusterStatus(ctx context.Context) (cluster.Status, error) {
	return clusterCall[cluster.Status](ctx, c, http.MethodGet, "/cluster", nil)
}

// ClusterInit creates a new cluster and returns its join token.
func (c *Client) ClusterInit(ctx context.Context, req proto.ClusterInitRequest) (string, error) {
	res, err := clusterCall[proto.ClusterTokenResponse](ctx, c, http.MethodPost, "/cluster/init", req)
	return res.Token, err
}

// ClusterJoin joins a cluster, blocking until approved or denied.
func (c *Client) ClusterJoin(ctx context.Context, req proto.ClusterJoinRequest) (cluster.Status, error) {
	if req.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutSeconds+10)*time.Second)
		defer cancel()
	}
	return clusterCall[cluster.Status](ctx, c, http.MethodPost, "/cluster/join", req)
}

// ClusterToken returns the current join token.
func (c *Client) ClusterToken(ctx context.Context) (string, error) {
	res, err := clusterCall[proto.ClusterTokenResponse](ctx, c, http.MethodGet, "/cluster/token", nil)
	return res.Token, err
}

// ClusterApprove admits a pending node.
func (c *Client) ClusterApprove(ctx context.Context, ref string) (cluster.Member, error) {
	return clusterCall[cluster.Member](ctx, c, http.MethodPost, "/cluster/approve", proto.ClusterRefRequest{Ref: ref})
}

// ClusterDeny rejects a pending node.
func (c *Client) ClusterDeny(ctx context.Context, ref string) (cluster.JoinRequest, error) {
	return clusterCall[cluster.JoinRequest](ctx, c, http.MethodPost, "/cluster/deny", proto.ClusterRefRequest{Ref: ref})
}

// ClusterRotateKey replaces the cluster key.
func (c *Client) ClusterRotateKey(ctx context.Context) (proto.ClusterTokenResponse, error) {
	return clusterCall[proto.ClusterTokenResponse](ctx, c, http.MethodPost, "/cluster/rotate", struct{}{})
}

// ClusterLeave removes this node from its cluster.
func (c *Client) ClusterLeave(ctx context.Context) (proto.ClusterTokenResponse, error) {
	return clusterCall[proto.ClusterTokenResponse](ctx, c, http.MethodPost, "/cluster/leave", struct{}{})
}

// ClusterUpgrade asks every other member to install version.
func (c *Client) ClusterUpgrade(ctx context.Context, version string) (proto.ClusterTokenResponse, error) {
	return clusterCall[proto.ClusterTokenResponse](ctx, c, http.MethodPost, "/cluster/upgrade", proto.ClusterUpgradeRequest{Version: version})
}

// ClusterSetName renames this node.
func (c *Client) ClusterSetName(ctx context.Context, name string) (cluster.Status, error) {
	return clusterCall[cluster.Status](ctx, c, http.MethodPost, "/cluster/name", proto.ClusterRefRequest{Ref: name})
}

// ClusterPing measures reachability of every other member.
func (c *Client) ClusterPing(ctx context.Context) ([]proto.ClusterPingResult, error) {
	return clusterCall[[]proto.ClusterPingResult](ctx, c, http.MethodGet, "/cluster/ping", nil)
}

// ClusterDashboard returns live health and resources for every node.
func (c *Client) ClusterDashboard(ctx context.Context) ([]cluster.NodeDashboard, error) {
	return clusterCall[[]cluster.NodeDashboard](ctx, c, http.MethodGet, "/cluster/dashboard", nil)
}
