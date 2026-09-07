package voice

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestForwardPCMDeliversBufferedThenLiveInOrder(t *testing.T) {
	t.Parallel()
	micCh := make(chan []byte, 8)
	audioReady := make(chan chan<- []byte, 1)
	audioCh := make(chan []byte, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		forwardPCMToSTT(ctx, micCh, audioReady)
		close(done)
	}()

	micCh <- []byte{1}
	micCh <- []byte{2}
	audioReady <- audioCh
	require.Equal(t, []byte{1}, <-audioCh)
	require.Equal(t, []byte{2}, <-audioCh)

	micCh <- []byte{3}
	require.Equal(t, []byte{3}, <-audioCh)

	close(micCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not exit")
	}
}

func TestForwardPCMReturnsWhenMicClosesBeforeConnect(t *testing.T) {
	t.Parallel()
	micCh := make(chan []byte)
	audioReady := make(chan chan<- []byte)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		forwardPCMToSTT(ctx, micCh, audioReady)
		close(done)
	}()
	close(micCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not exit")
	}
}

func TestForwardPCMReturnsWhenConnectFails(t *testing.T) {
	t.Parallel()
	micCh := make(chan []byte, 1)
	audioReady := make(chan chan<- []byte)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		forwardPCMToSTT(ctx, micCh, audioReady)
		close(done)
	}()
	micCh <- []byte{1}
	close(audioReady)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not exit")
	}
}
