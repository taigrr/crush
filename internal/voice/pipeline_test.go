package voice

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func collect(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	var got []byte
	deadline := time.After(2 * time.Second)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, chunk...)
		case <-deadline:
			t.Fatal("audio channel was not closed")
		}
	}
}

func TestBridgeDeliversBacklogThenLiveThenCloses(t *testing.T) {
	t.Parallel()
	mic := newPCMStream(16)
	audioReady := make(chan chan<- []byte, 1)
	audioCh := make(chan []byte, 8)
	go bridgePCM(t.Context(), mic.C(), audioReady)

	mic.push([]byte{1})
	mic.push([]byte{2})
	audioReady <- audioCh
	require.Equal(t, []byte{1}, <-audioCh)
	require.Equal(t, []byte{2}, <-audioCh)
	mic.push([]byte{3})
	require.Equal(t, []byte{3}, <-audioCh)

	mic.close()
	_, open := <-audioCh
	require.False(t, open, "bridge closes the audio channel when the microphone stream ends")
}

func TestBridgeFlushesBacklogWhenMicClosesBeforeSenderReceived(t *testing.T) {
	t.Parallel()
	mic := newPCMStream(16)
	audioReady := make(chan chan<- []byte, 1)
	audioCh := make(chan []byte, 64)
	for i := range 5 {
		mic.push([]byte{byte(i)})
	}
	// Sender and end-of-mic are both pending before the bridge even runs.
	audioReady <- audioCh
	mic.close()
	go bridgePCM(t.Context(), mic.C(), audioReady)
	require.Equal(t, []byte{0, 1, 2, 3, 4}, collect(t, audioCh), "every captured chunk reaches the server before audio.done")
}

func TestBridgeStragglersAfterStopAreDropped(t *testing.T) {
	t.Parallel()
	mic := newPCMStream(16)
	audioReady := make(chan chan<- []byte, 1)
	audioCh := make(chan []byte, 64)
	go bridgePCM(t.Context(), mic.C(), audioReady)
	audioReady <- audioCh
	mic.push([]byte{1})
	mic.close()
	for range 8 {
		mic.push([]byte{9}) // OS callback firing after Stop: must not panic.
	}
	require.Equal(t, []byte{1}, collect(t, audioCh))
}

func TestBridgeExitsWhenConnectFails(t *testing.T) {
	t.Parallel()
	mic := newPCMStream(16)
	audioReady := make(chan chan<- []byte)
	done := make(chan struct{})
	go func() {
		bridgePCM(t.Context(), mic.C(), audioReady)
		close(done)
	}()
	mic.push([]byte{1})
	close(audioReady)
	mic.close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit")
	}
}

func TestBridgeAbandonsOnCancel(t *testing.T) {
	t.Parallel()
	mic := newPCMStream(16)
	audioReady := make(chan chan<- []byte, 1)
	audioCh := make(chan []byte) // nobody reads: sends park
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		bridgePCM(ctx, mic.C(), audioReady)
		close(done)
	}()
	audioReady <- audioCh
	mic.push([]byte{1})
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit on cancel")
	}
}
