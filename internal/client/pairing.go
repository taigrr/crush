package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/taigrr/crush/internal/pairing"
)

// PairCreateOffer creates a node pairing offer (flow a).
func (c *Client) PairCreateOffer(ctx context.Context, req pairing.CreateOfferRequest) (pairing.OfferView, error) {
	return clusterCall[pairing.OfferView](ctx, c, http.MethodPost, "/cluster/pairing/offers", req)
}

// PairAccept registers a phone's payload or typed code (flow b).
func (c *Client) PairAccept(ctx context.Context, req pairing.AcceptRequest) (pairing.OfferView, error) {
	return clusterCall[pairing.OfferView](ctx, c, http.MethodPost, "/cluster/pairing/accept", req)
}

// PairWaitOffer returns the offer once its state differs from since, or
// after waitMS.
func (c *Client) PairWaitOffer(ctx context.Context, offerID string, since pairing.State, waitMS int) (pairing.OfferView, error) {
	q := url.Values{}
	if since != "" {
		q.Set("state", string(since))
		q.Set("wait_ms", fmt.Sprint(waitMS))
	}
	return clusterCallQuery[pairing.OfferView](ctx, c, http.MethodGet, "/cluster/pairing/offers/"+url.PathEscape(offerID), q, nil)
}

// PairApprove approves a claimed offer.
func (c *Client) PairApprove(ctx context.Context, offerID string, role string) (pairing.OfferView, error) {
	return clusterCall[pairing.OfferView](ctx, c, http.MethodPost, "/cluster/pairing/offers/"+url.PathEscape(offerID)+"/approve", pairing.ApproveRequest{Role: role})
}

// PairReject rejects an offer.
func (c *Client) PairReject(ctx context.Context, offerID string) (pairing.OfferView, error) {
	return clusterCall[pairing.OfferView](ctx, c, http.MethodPost, "/cluster/pairing/offers/"+url.PathEscape(offerID)+"/reject", struct{}{})
}

// Devices lists paired devices.
func (c *Client) Devices(ctx context.Context) ([]pairing.Device, error) {
	return clusterCall[[]pairing.Device](ctx, c, http.MethodGet, "/cluster/devices", nil)
}

// RevokeDevice revokes a device cluster-wide.
func (c *Client) RevokeDevice(ctx context.Context, deviceID string) (pairing.Device, error) {
	return clusterCall[pairing.Device](ctx, c, http.MethodPost, "/cluster/devices/"+url.PathEscape(deviceID)+"/revoke", struct{}{})
}
