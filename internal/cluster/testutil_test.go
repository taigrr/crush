package cluster

import (
	"crypto/tls"
	"net"
	"time"
)

type tlsCertificate = tls.Certificate

// tlsDial opens a raw TLS connection the way NATS routes do: plain TCP
// first (the server sends INFO), then TLS.
func tlsDial(addr string, cfg *tls.Config) (net.Conn, error) {
	raw, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return nil, err
	}
	_ = raw.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	if _, err := raw.Read(buf); err != nil {
		raw.Close()
		return nil, err
	}
	_ = raw.SetReadDeadline(time.Time{})
	c := tls.Client(raw, cfg)
	if err := c.Handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	return c, nil
}
