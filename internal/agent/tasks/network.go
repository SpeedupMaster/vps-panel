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

var ipRe = regexp.MustCompile(`\b(\d{1,3}\.){3}\d{1,3}\b`)

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
		var hops []string
		for _, l := range strings.Split(text, "\n") {
			if ip := ipRe.FindString(l); ip != "" && ip != host {
				hops = append(hops, ip)
			}
		}
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
	cl := &http.Client{Timeout: 60 * time.Second}

	const down = 200 << 20
	req, _ := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("https://speed.cloudflare.com/__down?bytes=%d", down), nil)
	start := time.Now()
	resp, err := cl.Do(req)
	if err != nil {
		return nil, err
	}
	n, _ := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	r.DownMbps = float64(n*8) / time.Since(start).Seconds() / 1e6
	out(fmt.Sprintf("下行 %.0f Mbps\n", r.DownMbps))

	const up = 50 << 20
	body := io.LimitReader(rand.Reader, up)
	req, _ = http.NewRequestWithContext(ctx, "POST", "https://speed.cloudflare.com/__up", body)
	req.ContentLength = up
	start = time.Now()
	resp, err = cl.Do(req)
	if err == nil {
		resp.Body.Close()
		r.UpMbps = float64(up*8) / time.Since(start).Seconds() / 1e6
		out(fmt.Sprintf("上行 %.0f Mbps\n", r.UpMbps))
	}
	r.DownMbps = float64(int(r.DownMbps*10)) / 10
	r.UpMbps = float64(int(r.UpMbps*10)) / 10
	return r, nil
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
