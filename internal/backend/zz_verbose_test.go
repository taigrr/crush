package backend

import (
	"flag"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	testing.Init()
	flag.Parse()
	_ = flag.Set("test.v", "true")
	os.Exit(m.Run())
}

func TestZZZForceOutput(t *testing.T) { t.Error("DEBUG force verbose output") }
