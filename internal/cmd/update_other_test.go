//go:build !windows

package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsCrushExeName_DeletedSuffixAndSelf(t *testing.T) {
	t.Parallel()
	require.True(t, isCrushExeName("/home/u/go/bin/crush"))
	require.True(t, isCrushExeName("/home/u/go/bin/crush.new"))
	require.False(t, isCrushExeName("/usr/bin/python3"))
	self, err := os.Executable()
	require.NoError(t, err)
	require.True(t, isCrushExeName(self), "the running binary (however named) is ours")
}
