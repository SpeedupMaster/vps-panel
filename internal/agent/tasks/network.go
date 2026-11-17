package tasks

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"vpspanel/internal/agent/prober"
)

// ---------- backhaul route trace ----------

type TraceResult struct {
	Carrier string   `json:"carrier"`
	Target  string   `json:"target"`
	Line    string   `json:"line"`
	Hops    []string `json:"hops"`
}

var (
	ipRe  = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)
	hopRe = regexp.MustCompile(`^\s*\d+\s`)
)

// parseHops returns the responding IP of each hop. Only numbered hop lines
// count: the "traceroute to host (ip)" header names the target, not a hop.
func parseHops(text string) []string {
	var hops []string
	for _, l := range strings.Split(text, "\n") {
		if !hopRe.MatchString(l) {
			continue
		}
		if ip := ipRe.FindString(l); ip != "" {
			hops = append(hops, ip)
		}
	}
	return hops
}

// classify guesses the carrier backbone from hop prefixes.
func classify(carrier string, hops []string) string {
	has := func(prefixes ...string) bool {
		for _, h := range hops {
			for _, p := range prefixes {
				if strings.HasPrefix(h, p) {
					return true
				}
			}
		}
		return false
	}
	switch carrier {
	case "telecom":
		switch {
		case has("59.43.") && !has("202.97."):
			return "CN2 GIA"
		case has("59.43."):
			return "CN2 GT"
		case has("202.97."):
			return "163"
		}
	case "unicom":
		switch {
		case has("218.105.", "210.51."):
			return "9929"
		case has("219.158."):
			return "4837"
		}
	case "mobile":
		switch {
		case has("223.120.", "223.118.", "223.119."):
			return "CMI"
		case has("221.183.", "221.176."):
			return "CMNET"
		}
	}
	return "未知"
}

// Trace runs nexttrace (preferred) or traceroute against carrier targets.
// args["targets"] = "telecom=host,unicom=host,mobile=host".
func Trace(ctx context.Context, args map[string]string, out func(string)) ([]TraceResult, error) {
	tool := ""
	switch {
	case have("nexttrace"):
		tool = "nexttrace"
	case have("traceroute"):
		tool = "traceroute"
	default:
		out("未找到 traceroute，尝试安装\n")
		for _, pm := range [][]string{{"apt-get", "install", "-y", "-qq", "traceroute"}, {"dnf", "install", "-y", "-q", "traceroute"}, {"apk", "add", "-q", "traceroute"}} {
			if have(pm[0]) {
				_ = exec.CommandContext(ctx, pm[0], pm[1:]...).Run()
				break
			}
		}
		if !have("traceroute") {
			return nil, errors.New("没有可用的 traceroute / nexttrace")
		}
		tool = "traceroute"
	}
	var res []TraceResult
	for _, pair := range strings.Split(args["targets"], ",") {
		carrier, host, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			continue
		}
		host, _, _ = strings.Cut(host, ":")
		out(fmt.Sprintf("== %s → %s\n", carrier, host))
		var text string
		var err error
		if tool == "nexttrace" {
			text, err = Output(ctx, "nexttrace", "-q", "1", "--no-color", "-g", "en", host)
		} else {
			text, err = Output(ctx, "traceroute", "-n", "-q", "1", "-w", "2", "-m", "25", host)
		}
		if err != nil && text == "" {
			out("失败：" + err.Error() + "\n")
			continue
		}
		hops := parseHops(text)
		r := TraceResult{Carrier: carrier, Target: host, Hops: hops, Line: classify(carrier, hops)}
		out(text + "线路：" + r.Line + "\n")
		res = append(res, r)
	}
	return res, nil
}

// ---------- speed test (Cloudflare) ----------

type SpeedResult struct {
	Server   string  `json:"server"`
	PingMs   float64 `json:"ping_ms"`
	DownMbps float64 `json:"down_mbps"`
	UpMbps   float64 `json:"up_mbps"`
}

func Speedtest(ctx context.Context, out func(string)) (*SpeedResult, error) {
	r := &SpeedResult{Server: "speed.cloudflare.com"}
	var rtts []float64
	for i := 0; i < 5; i++ {
		if d, err := prober.TCPPing("speed.cloudflare.com", 443, 3*time.Second); err == nil {
			rtts = append(rtts, float64(d.Microseconds())/1000)
		}
	}
	r.PingMs = prober.Summarize("", rtts, 5).Avg
	out(fmt.Sprintf("延迟 %.1f ms\n", r.PingMs))

	down, err := speedPhase(ctx, func(ctx context.Context, count *countWriter) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", speedChunkDown), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
		}
		_, err = io.Copy(count, resp.Body)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("下行测速失败：%w", err)
	}
	r.DownMbps = down
	out(fmt.Sprintf("下行 %.1f Mbps\n", down))

	up, err := speedPhase(ctx, func(ctx context.Context, count *countWriter) error {
		body := io.TeeReader(io.LimitReader(rand.Reader, speedChunkUp), count)
		req, _ := http.NewRequestWithContext(ctx, "POST", "https://speed.cloudflare.com/__up", body)
		req.ContentLength = speedChunkUp
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("上传返回 HTTP %d", resp.StatusCode)
		}
		return nil
	})
	if err != nil {
		out("上行测速失败：" + err.Error() + "\n")
	} else {
		r.UpMbps = up
		out(fmt.Sprintf("上行 %.1f Mbps\n", up))
	}
	return r, nil
}

// Cloudflare refuses single downloads somewhere under 100 MB, so the test
// runs a few parallel streams of moderate chunks for a fixed time instead of
// one big transfer.
const (
	speedChunkDown = 25_000_000
	speedChunkUp   = 5_000_000
	speedStreams   = 4
	speedDuration  = 10 * time.Second
)

type countWriter struct{ n atomic.Int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n.Add(int64(len(p))); return len(p), nil }

// speedPhase runs one() in speedStreams parallel loops for speedDuration and
// returns the measured Mbps. Stopping mid-chunk at the deadline is expected;
// any other failure before data flowed is returned.
func speedPhase(ctx context.Context, one func(context.Context, *countWriter) error) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, speedDuration)
	defer cancel()
	var count countWriter
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex
	start := time.Now()
	for i := 0; i < speedStreams; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if err := one(ctx, &count); err != nil {
					if ctx.Err() == nil {
						mu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						mu.Unlock()
						cancel()
					}
					return
				}
			}
		}()
	}
	wg.Wait()
	n := count.n.Load()
	if n == 0 && firstErr != nil {
		return 0, firstErr
	}
	if n == 0 {
		return 0, errors.New("没有收到数据")
	}
	mbps := float64(n*8) / time.Since(start).Seconds() / 1e6
	return float64(int(mbps*10)) / 10, nil
}

// ---------- iperf3 between VPS ----------

// Iperf runs iperf3 as a one-shot server (role=server) or as a client.
func Iperf(ctx context.Context, args map[string]string, out func(string)) (any, error) {
	if !have("iperf3") {
		out("安装 iperf3\n")
		for _, pm := range [][]string{{"apt-get", "install", "-y", "-qq", "iperf3"}, {"dnf", "install", "-y", "-q", "iperf3"}, {"apk", "add", "-q", "iperf3"}} {
			if have(pm[0]) {
				_ = exec.CommandContext(ctx, pm[0], pm[1:]...).Run()
				break
			}
		}
		if !have("iperf3") {
			return nil, errors.New("无法安装 iperf3")
		}
	}
	port := args["port"]
	if port == "" {
		port = "45201"
	}
	if args["role"] == "server" {
		// Open the port only for the peer while the test runs.
		_, err := RunStream(ctx, out, "iperf3", "-s", "-1", "-p", port)
		return nil, err
	}
	host := args["host"]
	if net.ParseIP(host) == nil {
		return nil, errors.New("缺少对端 IP")
	}
	var lastErr error
	for i := 0; i < 5; i++ { // wait for the server side to start
		text, err := Output(ctx, "iperf3", "-c", host, "-p", port, "-t", "10", "-J", "-R")
		if err == nil {
			var j struct {
				End struct {
					SumReceived struct {
						BitsPerSecond float64 `json:"bits_per_second"`
					} `json:"sum_received"`
				} `json:"end"`
			}
			_ = json.Unmarshal([]byte(text), &j)
			mbps := j.End.SumReceived.BitsPerSecond / 1e6
			out(fmt.Sprintf("%s → 本机 %.0f Mbps\n", host, mbps))
			return map[string]float64{"mbps": mbps}, nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	return nil, lastErr
}
