package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"strings"
)

// apiPrefix is where a node serves its local Crush API to members.
const apiPrefix = "/node/v1/api"

// SetAPIHandler exposes h (the server's local API) to cluster members
// under /node/v1/api. Members can fully drive this node — approve only
// machines you control.
func (n *Node) SetAPIHandler(h http.Handler) {
	n.apiMu.Lock()
	defer n.apiMu.Unlock()
	n.api = h
}

func (n *Node) handleAPI(w http.ResponseWriter, r *http.Request, _ Member) {
	n.apiMu.Lock()
	h := n.api
	n.apiMu.Unlock()
	if h == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("this node does not expose its API"))
		return
	}
	http.StripPrefix(apiPrefix, h).ServeHTTP(w, r)
}

// ResolveEndpoint returns the endpoint to reach member at: the cached
// one when it still works, re-measured against the member's other
// candidates in the background every peerReprobeInterval (or when its
// advertised addresses change), otherwise the fastest one that answers.
func (n *Node) ResolveEndpoint(ctx context.Context, m Member) (string, error) {
	candidates := memberDialCandidates(m)
	if cached, ok := n.endpoints.get(m.NodeID); ok {
		n.maybeReprobe(m, cached, candidates)
		return cached.addr, nil
	}
	return n.probeEndpoints(ctx, m, candidates)
}

// ProxyAPI forwards r to member nodeID's local API. The request path
// must already be relative to the API root (e.g. /v1/workspaces).
// Streaming responses (SSE) are flushed immediately.
func (n *Node) ProxyAPI(w http.ResponseWriter, r *http.Request, nodeID string) {
	st, err := n.store.Snapshot()
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	m, ok := st.Member(nodeID)
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Errorf("unknown cluster node %q", nodeID))
		return
	}
	if nodeID == n.id.ID {
		writeErr(w, http.StatusBadRequest, errors.New("refusing to proxy to self"))
		return
	}
	addr, err := n.ResolveEndpoint(r.Context(), m)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "https"
			pr.Out.URL.Host = addr
			pr.Out.URL.Path = apiPrefix + "/" + strings.TrimPrefix(pr.In.URL.Path, "/")
			pr.Out.URL.RawPath = ""
			pr.Out.Host = addr
			pr.Out.Header.Set(headerVersion, n.version)
			pr.Out.Header.Set(headerProtocol, fmt.Sprint(ProtocolVersion))
		},
		Transport:     n.proxyTransport(),
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				n.forgetEndpoint(m.NodeID)
			}
			if !errors.Is(err, context.Canceled) {
				slog.Warn("Cluster proxy error", "node", m.Name, "path", r.URL.Path, "error", err)
			}
			writeErr(w, http.StatusBadGateway, fmt.Errorf("node %s: %w", m.Name, err))
		},
	}
	rp.ServeHTTP(w, r)
}

// proxyTransport returns a shared keep-alive transport for API proxying.
// It has no overall timeout so event streams stay open.
func (n *Node) proxyTransport() http.RoundTripper {
	n.proxyOnce.Do(func() {
		_, clientTLS := tlsConfigs(n.currentCert, n.trustedKeys)
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = clientTLS
		tr.ForceAttemptHTTP2 = true
		tr.ResponseHeaderTimeout = 0
		n.proxyRT = tr
	})
	return n.proxyRT
}

// CallAPI performs a request against member m's local Crush API (path
// relative to the API root, e.g. /v1/version) and decodes the JSON
// response into out.
func (n *Node) CallAPI(ctx context.Context, m Member, method, path string, out any) error {
	addr, err := n.ResolveEndpoint(ctx, m)
	if err != nil {
		return err
	}
	hc := &http.Client{Transport: n.proxyTransport()}
	_, err = n.call(ctx, hc, addr, method, apiPrefix+path, nil, out)
	n.forgetOnTransportError(m.NodeID, err)
	return err
}

// Members returns every member except this node.
func (n *Node) Members() []Member { return n.otherMembers() }

// CallAPIJSON is [Node.CallAPI] with a JSON request body.
func (n *Node) CallAPIJSON(ctx context.Context, m Member, method, path string, in, out any) error {
	addr, err := n.ResolveEndpoint(ctx, m)
	if err != nil {
		return err
	}
	hc := &http.Client{Transport: n.proxyTransport()}
	_, err = n.call(ctx, hc, addr, method, apiPrefix+path, in, out)
	n.forgetOnTransportError(m.NodeID, err)
	return err
}

// StreamAPI POSTs body (of contentType) to member m's local API and
// decodes the JSON response into out. The body is streamed, never
// buffered, so it suits multi-gigabyte transfers.
func (n *Node) StreamAPI(ctx context.Context, m Member, path, contentType string, body io.Reader, out any) error {
	addr, err := n.ResolveEndpoint(ctx, m)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+addr+apiPrefix+path, body)
	if err != nil {
		return err
	}
	req.Header.Set(headerVersion, n.version)
	req.Header.Set(headerProtocol, fmt.Sprint(ProtocolVersion))
	req.Header.Set("Content-Type", contentType)
	rsp, err := (&http.Client{Transport: n.proxyTransport()}).Do(req)
	if err != nil {
		n.forgetOnTransportError(m.NodeID, err)
		return err
	}
	defer rsp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(rsp.Body, 8<<20))
	if err != nil {
		return err
	}
	if rsp.StatusCode < 200 || rsp.StatusCode > 299 {
		var eb errorBody
		_ = json.Unmarshal(data, &eb)
		msg := eb.Error
		if msg == "" {
			msg = eb.Message
		}
		return &statusError{Status: rsp.StatusCode, Msg: msg}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// MemberByID returns the member with nodeID.
func (n *Node) MemberByID(nodeID string) (Member, error) {
	st, err := n.store.Snapshot()
	if err != nil {
		return Member{}, err
	}
	m, ok := st.Member(nodeID)
	if !ok {
		return Member{}, fmt.Errorf("unknown cluster node %q", nodeID)
	}
	return m, nil
}

// forgetEndpoint drops the cached endpoint for nodeID after a failure.
func (n *Node) forgetEndpoint(nodeID string) {
	n.endpoints.forget(nodeID)
}

// forgetOnTransportError clears the cached endpoint when err means the
// node could not be reached (as opposed to the node answering an error).
func (n *Node) forgetOnTransportError(nodeID string, err error) {
	var se *statusError
	if err != nil && !errors.As(err, &se) && !errors.Is(err, context.Canceled) {
		n.forgetEndpoint(nodeID)
	}
}
