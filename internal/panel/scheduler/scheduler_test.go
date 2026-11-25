package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerWaitWaitsForRunningJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx)

	started := make(chan struct{})
	release := make(chan struct{})
	var finished atomic.Bool

	s.Every("slow-job", time.Hour, true, func() {
		close(started)
		<-release
		finished.Store(true)
	})

	<-started
	cancel()

	waitDone := make(chan struct{})
	go func() {
		s.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
		t.Fatal("Wait() returned before running job finished")
	case <-time.After(40 * time.Millisecond):
	}

	close(release)
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("Wait() did not return after job completed")
	}
	if !finished.Load() {
		t.Fatal("job did not finish")
	}
}
