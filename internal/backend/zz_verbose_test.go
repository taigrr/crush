package backend

import (
	"flag"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	flag.Parse()
	_ = flag.Set("test.v", "true")
	os.Exit(m.Run())
}
