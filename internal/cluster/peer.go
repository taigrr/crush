package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// statusError is a non-2xx response from a peer.
type statusError struct {
	Status int
	Msg    string
}

func (e *statusError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("peer returned HTTP %d", e.Status)
}

// call performs one cluster RPC against addr and decodes a JSON
// response into out (when non-nil). The returned status is the HTTP
// status code of a successful (2xx) response.
func (n *Node) call(ctx context.Context, hc *http.Client, addr, method, path string, in, out any) (int, error) {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set(headerVersion, n.version)
	req.Header.Set(headerProtocol, strconv.Itoa(ProtocolVersion))
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rsp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer rsp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(rsp.Body, 8<<20))
	if err != nil {
		return 0, err
	}
	if rsp.StatusCode < 200 || rsp.StatusCode > 299 {
		var eb errorBody
		_ = json.Unmarshal(data, &eb)
		if rsp.StatusCode == http.StatusForbidden && out != nil {
			// Denied joins carry a structured body.
			_ = json.Unmarshal(data, out)
		}
		msg := eb.Error
		if msg == "" {
			msg = eb.Message
		}
		return rsp.StatusCode, &statusError{Status: rsp.StatusCode, Msg: msg}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return rsp.StatusCode, fmt.Errorf("decode peer response: %w", err)
		}
	}
	return rsp.StatusCode, nil
}

// callMember tries each of a member's endpoints until one answers.
func (n *Node) callMember(ctx context.Context, m Member, method, path string, in, out any) error {
	if len(m.Endpoints) == 0 {
		return fmt.Errorf("member %s has no known endpoints", m.Name)
	}
	hc := n.httpClient()
	var errs []error
	for _, ep := range m.Endpoints {
		if ep.Kind != EndpointDirect {
			continue
		}
		_, err := n.call(ctx, hc, ep.Addr, method, path, in, out)
		if err == nil {
			return nil
		}
		var se *statusError
		if errors.As(err, &se) {
			// The peer answered; other endpoints reach the same node.
			return fmt.Errorf("%s: %w", m.Name, err)
		}
		errs = append(errs, fmt.Errorf("%s: %w", ep.Addr, err))
	}
	return fmt.Errorf("member %s unreachable: %w", m.Name, errors.Join(errs...))
}

// otherMembers returns every member except this node.
func (n *Node) otherMembers() []Member {
	st, err := n.store.Snapshot()
	if err != nil {
		return nil
	}
	var out []Member
	for _, m := range st.Members {
		if m.NodeID != n.id.ID {
			out = append(out, m)
		}
	}
	return out
}
