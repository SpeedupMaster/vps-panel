// Package scheduler runs periodic jobs: fixed intervals and daily "HH:MM".
package scheduler

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"
)

type Scheduler struct {
	ctx context.Context
}

func New(ctx context.Context) *Scheduler { return &Scheduler{ctx: ctx} }

// Every runs fn every d (first run after d, or immediately when now is true).
func (s *Scheduler) Every(name string, d time.Duration, now bool, fn func()) {
	go func() {
		if now {
			safe(name, fn)
		}
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-t.C:
				safe(name, fn)
			}
		}
	}()
}

// Daily runs fn once a day at the time returned by at() ("HH:MM", local).
// at is re-read every minute so settings changes apply without restart.
func (s *Scheduler) Daily(name string, at func() string, fn func()) {
	go func() {
		last := ""
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case now := <-t.C:
				h, m, ok := ParseHM(at())
				if !ok {
					continue
				}
				day := now.Format("2006-01-02")
				if now.Hour() == h && now.Minute() == m && last != day {
					last = day
					go safe(name, fn)
				}
			}
		}
	}()
}

func ParseHM(s string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, 0, false
	}
	return h, m, true
}

func safe(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("scheduler: job %s panicked: %v", name, r)
		}
	}()
	fn()
}
