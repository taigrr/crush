package server

import (
	"os"
	"testing"

	"github.com/taigrr/crush/internal/cluster"
)

// TestMain pins the host interfaces device listeners are advertised on,
// so advertised addresses do not depend on the machine running the tests.
func TestMain(m *testing.M) {
	deviceInterfaces = func() ([]cluster.NetInterface, error) { return nil, nil }
	os.Exit(m.Run())
}
