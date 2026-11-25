package scheduler

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseHM(t *testing.T) {
	cases := []struct {
		in   string
		h, m int
		ok   bool
	}{
		{"00:00", 0, 0, true},
		{"04:30", 4, 30, true},
		{" 23:59 ", 23, 59, true},
		{"24:00", 0, 0, false},
		{"12:60", 0, 0, false},
		{"-1:00", 0, 0, false},
		{"bad", 0, 0, false},
		{"12:30:00", 0, 0, false},
	}
	for _, tc := range cases {
		h, m, ok := ParseHM(tc.in)
		if h != tc.h || m != tc.m || ok != tc.ok {
			t.Errorf("ParseHM(%q) = (%d,%d,%v), want (%d,%d,%v)", tc.in, h, m, ok, tc.h, tc.m, tc.ok)
		}
	}
}

func TestEveryPeriodicAndPanicRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx)

	var count atomic.Int32
	done := make(chan struct{})
	s.Every("ticker", 10*time.Millisecond, true, func() {
		n := count.Add(1)
		if n == 1 {
			panic("simulated panic")
		}
		if n == 3 {
			close(done)
		}
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Every did not survive panic / reach 3 ticks (got %d)", count.Load())
	}
	cancel()
	s.Wait()
}

func TestDailyNonOverlapAndWait(t *testing.T) {
	prevTick := dailyTick
	dailyTick = 10 * time.Millisecond
	t.Cleanup(func() { dailyTick = prevTick })

	ctx, cancel := context.WithCancel(context.Background())
	s := New(ctx)

	now := time.Now()
	hm := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())

	started := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	var inFlight atomic.Int32
	var overlap atomic.Bool

	s.Daily("daily-job", func() string { return hm }, func() {
		if inFlight.Add(1) > 1 {
			overlap.Store(true)
		}
		defer inFlight.Add(-1)
		if runs.Add(1) == 1 {
			close(started)
			<-release
		}
	})

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Daily job did not trigger")
	}

	// Wait several ticks while the first run is still active to ensure no overlap.
	time.Sleep(50 * time.Millisecond)
	if overlap.Load() || runs.Load() != 1 {
		t.Fatalf("Daily overlapped or ran multiple times same day: runs=%d overlap=%v", runs.Load(), overlap.Load())
	}

	// Cancel context while Daily's spawned goroutine is still blocked; Wait() must block until release.
	cancel()
	waitDone := make(chan struct{})
	go func() {
		s.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
		t.Fatal("Wait() returned before Daily job finished")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("Wait() did not return after Daily job finished")
	}
}

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
