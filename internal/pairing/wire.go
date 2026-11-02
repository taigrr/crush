package pairing

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

// Bytes is a byte slice encoded as base64url without padding in JSON, as
// the spec requires for every binary value.
type Bytes []byte

// MarshalJSON implements [json.Marshaler].
func (b Bytes) MarshalJSON() ([]byte, error) {
	return json.Marshal(base64.RawURLEncoding.EncodeToString(b))
}

// UnmarshalJSON implements [json.Unmarshaler].
func (b *Bytes) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return err
	}
	*b = raw
	return nil
}

// ErrorCode is a machine-readable pairing error.
type ErrorCode string

// Pairing error codes.
const (
	ErrCodeBadRequest   ErrorCode = "pair_bad_request"
	ErrCodeMACInvalid   ErrorCode = "pair_mac_invalid"
	ErrCodeOfferUnknown ErrorCode = "pair_offer_unknown"
	ErrCodeOfferUsed    ErrorCode = "pair_offer_used"
	ErrCodeOfferExpired ErrorCode = "pair_offer_expired"
	ErrCodeOfferAborted ErrorCode = "pair_offer_aborted"
	ErrCodeRateLimited  ErrorCode = "pair_rate_limited"
	ErrCodeNotInCluster ErrorCode = "pair_not_in_cluster"
	ErrCodeInternal     ErrorCode = "pair_internal"
)

var errorStatus = map[ErrorCode]int{
	ErrCodeBadRequest:   http.StatusBadRequest,
	ErrCodeMACInvalid:   http.StatusUnauthorized,
	ErrCodeOfferUnknown: http.StatusNotFound,
	ErrCodeOfferUsed:    http.StatusConflict,
	ErrCodeOfferExpired: http.StatusGone,
	ErrCodeOfferAborted: http.StatusGone,
	ErrCodeRateLimited:  http.StatusTooManyRequests,
	ErrCodeNotInCluster: http.StatusConflict,
	ErrCodeInternal:     http.StatusInternalServerError,
}

// Error is a pairing protocol error; it is also the JSON error body.
type Error struct {
	Code    ErrorCode `json:"error" enums:"pair_bad_request,pair_mac_invalid,pair_offer_unknown,pair_offer_used,pair_offer_expired,pair_offer_aborted,pair_rate_limited,pair_not_in_cluster,pair_internal"`
	Message string    `json:"message"`
} //@name pairing.Error

func (e *Error) Error() string { return e.Message }

// Status returns the HTTP status for the error.
func (e *Error) Status() int {
	if s, ok := errorStatus[e.Code]; ok {
		return s
	}
	return http.StatusBadRequest
}

func pairErr(code ErrorCode, msg string) *Error { return &Error{Code: code, Message: msg} }

// State is an offer state.
type State string

// Offer states.
const (
	StateOpen     State = "open"
	StateClaimed  State = "claimed"
	StateApproved State = "approved"
	StateExpired  State = "expired"
	StateAborted  State = "aborted"
	StateRejected State = "rejected"
)

// Terminal reports whether no further transition is possible.
func (s State) Terminal() bool {
	return s == StateApproved || s == StateExpired || s == StateAborted || s == StateRejected
}

// Abort reasons.
const (
	ReasonSASMismatch = "sas_mismatch"
	ReasonUserCancel  = "user_cancel"
	ReasonDoubleClaim = "double_claim"
	ReasonBadMAC      = "bad_mac"
	ReasonRejected    = "operator_rejected"
)

// InfoEndpoint is one endpoint in /client/v1/info.
type InfoEndpoint struct {
	Kind string `json:"kind"`
	Addr string `json:"addr"`
} //@name pairing.InfoEndpoint

// Info is the GET /client/v1/info response.
type Info struct {
	ClientProto int            `json:"client_proto"`
	ClusterID   string         `json:"cluster_id"`
	NodeID      string         `json:"node_id"`
	NodeName    string         `json:"node_name"`
	NodePub     Bytes          `json:"node_pub" swaggertype:"string"`
	CAFP        Bytes          `json:"ca_fp" swaggertype:"string"`
	Endpoints   []InfoEndpoint `json:"endpoints"`
} //@name pairing.Info

// ClaimRequest is the POST /client/v1/pair/claim body.
type ClaimRequest struct {
	OfferID     Bytes  `json:"offer_id" swaggertype:"string"`
	DevicePub   Bytes  `json:"device_pub" swaggertype:"string"`
	DeviceName  string `json:"device_name"`
	Platform    string `json:"platform"`
	Model       string `json:"model,omitempty"`
	AppVersion  string `json:"app_version,omitempty"`
	NonceDevice Bytes  `json:"nonce_device" swaggertype:"string"`
	MAC         Bytes  `json:"mac" swaggertype:"string"`
	Sig         Bytes  `json:"sig" swaggertype:"string"`
} //@name pairing.ClaimRequest

// ClaimResponse is the claim reply.
type ClaimResponse struct {
	OfferID   Bytes     `json:"offer_id" swaggertype:"string"`
	State     State     `json:"state"`
	NonceNode Bytes     `json:"nonce_node" swaggertype:"string"`
	MAC       Bytes     `json:"mac" swaggertype:"string"`
	Sig       Bytes     `json:"sig" swaggertype:"string"`
	ExpiresAt time.Time `json:"expires_at"`
} //@name pairing.ClaimResponse

// ConfirmRequest is the POST /client/v1/pair/confirm body.
type ConfirmRequest struct {
	OfferID Bytes `json:"offer_id" swaggertype:"string"`
	MAC     Bytes `json:"mac" swaggertype:"string"`
	Sig     Bytes `json:"sig" swaggertype:"string"`
} //@name pairing.ConfirmRequest

// StateResponse carries an offer's state.
type StateResponse struct {
	OfferID Bytes `json:"offer_id" swaggertype:"string"`
	State   State `json:"state"`
} //@name pairing.StateResponse

// PollRequest is the POST /client/v1/pair/poll body.
type PollRequest struct {
	OfferID Bytes `json:"offer_id" swaggertype:"string"`
	MAC     Bytes `json:"mac" swaggertype:"string"`
	WaitMS  int   `json:"wait_ms,omitempty"`
} //@name pairing.PollRequest

// NATSCredential describes the scoped NATS user issued to a device. The
// secret half is the device's own key and is never transmitted.
type NATSCredential struct {
	User        string          `json:"user"`
	Account     string          `json:"account"`
	Permissions NATSPermissions `json:"permissions"`
	URLs        []string        `json:"urls"`
} //@name pairing.NATSCredential

// MemberInfo is a cluster member as shown to devices.
type MemberInfo struct {
	NodeID    string         `json:"node_id"`
	Name      string         `json:"name"`
	NodePub   Bytes          `json:"node_pub" swaggertype:"string"`
	Endpoints []InfoEndpoint `json:"endpoints"`
} //@name pairing.MemberInfo

// PollResponse is the poll reply.
type PollResponse struct {
	OfferID Bytes           `json:"offer_id" swaggertype:"string"`
	State   State           `json:"state"`
	Device  *Device         `json:"device,omitempty"`
	NATS    *NATSCredential `json:"nats,omitempty"`
	CAFPs   []Bytes         `json:"ca_fps,omitempty" swaggertype:"array,string"`
	// CACerts are the cluster CA certificates (DER), current first. A
	// device trusts one only if its SPKI hash is a pinned fingerprint.
	CACerts []Bytes      `json:"ca_certs,omitempty" swaggertype:"array,string"`
	Members []MemberInfo `json:"members,omitempty"`
} //@name pairing.PollResponse

// AbortRequest is the POST /client/v1/pair/abort body.
type AbortRequest struct {
	OfferID Bytes  `json:"offer_id" swaggertype:"string"`
	Reason  string `json:"reason" enums:"sas_mismatch,user_cancel"`
	MAC     Bytes  `json:"mac" swaggertype:"string"`
} //@name pairing.AbortRequest

// SASView is the SAS as rendered for operators.
type SASView struct {
	Emoji  []string `json:"emoji"`
	Names  []string `json:"names"`
	Digits string   `json:"digits"`
} //@name pairing.SASView

// DeviceMeta is display-only metadata a device sent with its claim.
type DeviceMeta struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	Platform   string `json:"platform,omitempty"`
	Model      string `json:"model,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
} //@name pairing.DeviceMeta

// OfferView is an offer as shown on the local API.
type OfferView struct {
	OfferID     string      `json:"offer_id"`
	Kind        Kind        `json:"kind"`
	State       State       `json:"state"`
	Reason      string      `json:"reason,omitempty"`
	Role        Role        `json:"role"`
	Payload     string      `json:"payload,omitempty"`
	InfoPayload string      `json:"info_payload,omitempty"`
	ExpiresAt   time.Time   `json:"expires_at"`
	Device      *DeviceMeta `json:"device,omitempty"`
	SAS         *SASView    `json:"sas,omitempty"`
	DeviceOK    bool        `json:"device_confirmed"`
	OperatorOK  bool        `json:"operator_approved"`
} //@name pairing.OfferView

// CreateOfferRequest is the POST /v1/cluster/pairing/offers body.
type CreateOfferRequest struct {
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
	Role       string `json:"role,omitempty"`
} //@name pairing.CreateOfferRequest

// AcceptRequest is the POST /v1/cluster/pairing/accept body.
type AcceptRequest struct {
	Code string `json:"code"`
	Role string `json:"role,omitempty"`
} //@name pairing.AcceptRequest

// ApproveRequest is the approve body.
type ApproveRequest struct {
	Role string `json:"role,omitempty"`
} //@name pairing.ApproveRequest
