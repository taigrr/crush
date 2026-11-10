//go:build !windows

package server

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

const staleSocketProbeTimeout = 200 * time.Millisecond

func listen(network, address string) (net.Listener, error) {
	//nolint:noctx
	ln, err := net.Listen(network, address)
	if err == nil || network != "unix" || !isAddrInUse(err) {
		return ln, err
	}
	if _, statErr := os.Stat(address); statErr != nil {
		return nil, err
	}
	//nolint:noctx
	conn, dialErr := net.DialTimeout("unix", address, staleSocketProbeTimeout)
	if dialErr == nil {
		_ = conn.Close()
		return nil, err
	}
	if rmErr := os.Remove(address); rmErr != nil {
		return nil, fmt.Errorf("remove stale socket %s: %w", address, rmErr)
	}
	//nolint:noctx
	ln, err = net.Listen(network, address)
	if err != nil {
		return nil, fmt.Errorf("listen after removing stale socket %s: %w", address, err)
	}
	return ln, nil
}

func isAddrInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, os.ErrExist)
}
