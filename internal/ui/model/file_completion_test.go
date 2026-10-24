package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/taigrr/crush/internal/message"
)

func TestHandleFileCompletionRecordsReadOnModel(t *testing.T) {
	t.Parallel()
	m := &UI{}

	require.Nil(t, m.handleFileCompletion(fileCompletionMsg{absPath: "/a"}))
	require.Equal(t, []string{"/a"}, m.sessionFileReads)

	attachment := &message.Attachment{FilePath: "/a", Content: []byte("x")}
	cmd := m.handleFileCompletion(fileCompletionMsg{absPath: "/a", attachment: attachment})
	require.Equal(t, []string{"/a"}, m.sessionFileReads, "a repeated read is recorded once")
	require.NotNil(t, cmd)
	require.Equal(t, *attachment, cmd())
}
