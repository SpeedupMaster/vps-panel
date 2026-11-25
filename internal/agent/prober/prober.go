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
	"strconv"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
)

// TCPPing measures connect time once.
func TCPPing(host string, port int, timeout time.Duration) (time.Duration, error) {
	return TCPPingContext(context.Background(), host, port, timeout)
}

// TCPPingContext measures TCP connect time once, respecting ctx cancellation.
func TCPPingContext(ctx context.Context, host string, port int, timeout time.Duration) (time.Duration, error) {
	d := &net.Dialer{Timeout: timeout}
	start := time.Now()
	c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return 0, err
	}
	dur := time.Since(start)
	c.Close()
	return dur, nil
}

var pingRe = regexp.MustCompile(`time[=<]([\d.]+) ?ms`)

// ICMPPing uses the system ping binary (one echo).
func ICMPPing(host string, timeout time.Duration) (time.Duration, error) {
	return ICMPPingContext(context.Background(), host, timeout)
}

// ICMPPingContext runs one system ping echo, respecting ctx cancellation.
func ICMPPingContext(ctx context.Context, host string, timeout time.Duration) (time.Duration, error) {
	host = strings.TrimSpace(host)
	if host == "" || strings.HasPrefix(host, "-") {
		return 0, fmt.Errorf("invalid host %q", host)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout+time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "ping", "-c", "1", "-W", strconv.Itoa(int(math.Ceil(timeout.Seconds()))), "--", host).Output()
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

var httpPingClient = &http.Client{
	Transport: &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{}},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// HTTPPing times a GET (connection reuse disabled).
func HTTPPing(url string, timeout time.Duration) (time.Duration, error) {
	return HTTPPingContext(context.Background(), url, timeout)
}

// HTTPPingContext times a GET, respecting ctx cancellation.
func HTTPPingContext(ctx context.Context, url string, timeout time.Duration) (time.Duration, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := httpPingClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return 0, fmt.Errorf("status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

func onceContext(ctx context.Context, t proto.ProbeTarget, timeout time.Duration) (time.Duration, error) {
	switch t.Type {
	case "icmp":
		return ICMPPingContext(ctx, t.Host, timeout)
	case "http":
		return HTTPPingContext(ctx, t.URL, timeout)
	default:
		return TCPPingContext(ctx, t.Host, t.Port, timeout)
	}
}

// Measure runs count probes against a target and summarizes them.
func Measure(t proto.ProbeTarget, count int, timeout time.Duration) proto.ProbeSample {
	return MeasureContext(context.Background(), t, count, timeout)
}

// MeasureContext runs count probes against a target, stopping early if ctx cancels.
func MeasureContext(ctx context.Context, t proto.ProbeTarget, count int, timeout time.Duration) proto.ProbeSample {
	if count <= 0 {
		count = 5
	}
	var rtts []float64
	for i := 0; i < count; i++ {
		if ctx.Err() != nil {
			break
		}
		d, err := onceContext(ctx, t, timeout)
		if err == nil {
			rtts = append(rtts, float64(d.Microseconds())/1000)
		}
		if i < count-1 {
			select {
			case <-ctx.Done():
				return Summarize(t.ID, rtts, count)
			case <-time.After(200 * time.Millisecond):
			}
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
	var sum, jit float64
	minRTT, maxRTT := rtts[0], rtts[0]
	for i, r := range rtts {
		sum += r
		if r < minRTT {
			minRTT = r
		}
		if r > maxRTT {
			maxRTT = r
		}
		if i > 0 {
			jit += math.Abs(r - rtts[i-1])
		}
	}
	s.Avg = round(sum / float64(len(rtts)))
	s.Min, s.Max = round(minRTT), round(maxRTT)
	if len(rtts) > 1 {
		s.Jitter = round(jit / float64(len(rtts)-1))
	}
	return s
}

func round(f float64) float64 { return math.Round(f*100) / 100 }

// MeasureAll probes targets concurrently (bounded) and groups by kind.
func MeasureAll(targets []proto.ProbeTarget, count int, timeout time.Duration) map[string][]proto.ProbeSample {
	return MeasureAllContext(context.Background(), targets, count, timeout)
}

// MeasureAllContext probes targets concurrently while respecting ctx cancellation.
func MeasureAllContext(ctx context.Context, targets []proto.ProbeTarget, count int, timeout time.Duration) map[string][]proto.ProbeSample {
	out := map[string][]proto.ProbeSample{}
	var mu sync.Mutex
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Done()
			continue
		}
		go func(t proto.ProbeTarget) {
			defer wg.Done()
			defer func() { <-sem }()
			s := MeasureContext(ctx, t, count, timeout)
			if ctx.Err() != nil {
				return
			}
			mu.Lock()
			out[t.Kind] = append(out[t.Kind], s)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	return out
}
