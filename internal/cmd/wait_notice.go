package cmd

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// waitNoticeDelay is how long a startup step may take before a run that
// promised to return quickly (--detach) says what it is waiting on.
const waitNoticeDelay = time.Second

type waitNoticeKey struct{}

type waitNoticeTarget struct {
	w     io.Writer
	delay time.Duration
}

// withWaitNotices makes [noticeWhileWaiting] report slow steps on w.
func withWaitNotices(ctx context.Context, w io.Writer, delay time.Duration) context.Context {
	return context.WithValue(ctx, waitNoticeKey{}, waitNoticeTarget{w: w, delay: delay})
}

// noticeWhileWaiting prints "Waiting for <what>..." to the writer set by
// [withWaitNotices] if the returned stop function is not called within
// the configured delay. Without a writer on ctx it does nothing, so
// interactive and foreground commands keep their output unchanged.
func noticeWhileWaiting(ctx context.Context, what string) (stop func()) {
	target, ok := ctx.Value(waitNoticeKey{}).(waitNoticeTarget)
	if !ok || target.w == nil {
		return func() {}
	}
	var mu sync.Mutex
	stopped := false
	timer := time.AfterFunc(target.delay, func() {
		mu.Lock()
		defer mu.Unlock()
		if !stopped {
			fmt.Fprintf(target.w, "Waiting for %s...\n", what)
		}
	})
	return func() {
		mu.Lock()
		defer mu.Unlock()
		stopped = true
		timer.Stop()
	}
}
