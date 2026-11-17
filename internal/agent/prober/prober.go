// Package prober runs latency probes: TCP handshake timing (works without
// root and is what the zstaticcdn nodes expose), ICMP via the system ping,
// and HTTP request timing.
package prober

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
)

// TCPPing measures connect time once.
func TCPPing(host string, port int, timeout time.Duration) (time.Duration, error) {
	start := time.Now()
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), timeout)
	if err != nil {
		return 0, err
	}
	d := time.Since(start)
	c.Close()
	return d, nil
}

var pingRe = regexp.MustCompile(`time[=<]([\d.]+) ?ms`)

// ICMPPing uses the system ping binary (one echo).
func ICMPPing(host string, timeout time.Duration) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ping", "-c", "1", "-W", strconv.Itoa(int(math.Ceil(timeout.Seconds()))), host).Output()
	if err != nil {
		return 0, err
	}
	m := pingRe.FindSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("no reply")
	}
	ms, _ := strconv.ParseFloat(string(m[1]), 64)
	return time.Duration(ms * float64(time.Millisecond)), nil
}

// HTTPPing times a GET (connection reuse disabled).
func HTTPPing(url string, timeout time.Duration) (time.Duration, error) {
	tr := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{}}
	cl := &http.Client{Timeout: timeout, Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	start := time.Now()
	resp, err := cl.Get(url)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

func once(t proto.ProbeTarget, timeout time.Duration) (time.Duration, error) {
	switch t.Type {
	case "icmp":
		return ICMPPing(t.Host, timeout)
	case "http":
		return HTTPPing(t.URL, timeout)
	default:
		return TCPPing(t.Host, t.Port, timeout)
	}
}

// Measure runs count probes against a target and summarizes them.
func Measure(t proto.ProbeTarget, count int, timeout time.Duration) proto.ProbeSample {
	if count <= 0 {
		count = 5
	}
	var rtts []float64
	for i := 0; i < count; i++ {
		d, err := once(t, timeout)
		if err == nil {
			rtts = append(rtts, float64(d.Microseconds())/1000)
		}
		if i < count-1 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return Summarize(t.ID, rtts, count)
}

// Summarize computes avg/min/max/jitter/loss.
func Summarize(id string, rtts []float64, sent int) proto.ProbeSample {
	s := proto.ProbeSample{Target: id, Avg: -1, Loss: 1}
	if sent == 0 {
		return s
	}
	s.Loss = 1 - float64(len(rtts))/float64(sent)
	if len(rtts) == 0 {
		return s
	}
	sort.Float64s(rtts)
	var sum, jit float64
	for i, r := range rtts {
		sum += r
		if i > 0 {
			jit += math.Abs(r - rtts[i-1])
		}
	}
	s.Avg = round(sum / float64(len(rtts)))
	s.Min, s.Max = round(rtts[0]), round(rtts[len(rtts)-1])
	if len(rtts) > 1 {
		s.Jitter = round(jit / float64(len(rtts)-1))
	}
	return s
}

func round(f float64) float64 { return math.Round(f*100) / 100 }

// MeasureAll probes targets concurrently (bounded) and groups by kind.
func MeasureAll(targets []proto.ProbeTarget, count int, timeout time.Duration) map[string][]proto.ProbeSample {
	out := map[string][]proto.ProbeSample{}
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t proto.ProbeTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			s := Measure(t, count, timeout)
			mu.Lock()
			out[t.Kind] = append(out[t.Kind], s)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return out
}
