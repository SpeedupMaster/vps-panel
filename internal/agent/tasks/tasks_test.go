package tasks

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRunStreamHelperProcess is invoked as a subprocess by TestRunStreamTimeoutKillsChildProcess.
func TestRunStreamHelperProcess(t *testing.T) {
	role := os.Getenv("VPSAGENT_TEST_RUNSTREAM_ROLE")
	switch role {
	case "parent":
		// Spawn a grandchild inheriting stdout/stderr and sleeping for 30s.
		child := exec.Command(os.Args[0], "-test.run=^TestRunStreamHelperProcess$")
		child.Env = append(os.Environ(), "VPSAGENT_TEST_RUNSTREAM_ROLE=grandchild")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err == nil {
			fmt.Printf("parent-started pid=%d\n", child.Process.Pid)
		} else {
			fmt.Println("parent-started")
		}
		time.Sleep(30 * time.Second)
		os.Exit(0)
	case "grandchild":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func TestRunStreamTimeoutKillsChildProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	t.Setenv("VPSAGENT_TEST_RUNSTREAM_ROLE", "parent")
	var mu sync.Mutex
	var lines []string
	start := time.Now()
	_, err := RunStream(ctx, func(s string) {
		mu.Lock()
		lines = append(lines, s)
		mu.Unlock()
	}, os.Args[0], "-test.run=^TestRunStreamHelperProcess$")
	elapsed := time.Since(start)

	mu.Lock()
	joined := strings.Join(lines, "")
	mu.Unlock()

	// Ensure any non-Linux grandchild is terminated before `go test` unlinks the test binary.
	for _, f := range strings.Fields(joined) {
		if v, ok := strings.CutPrefix(f, "pid="); ok {
			if pid, perr := strconv.Atoi(v); perr == nil && pid > 0 {
				if p, ferr := os.FindProcess(pid); ferr == nil {
					_ = p.Kill()
					_, _ = p.Wait()
				}
			}
		}
	}

	if err == nil {
		t.Fatal("expected timeout/kill error from RunStream")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("RunStream hung waiting on child/grandchild: elapsed %s", elapsed)
	}
	if !strings.Contains(joined, "parent-started") {
		t.Fatalf("expected streamed output before timeout, got %q", joined)
	}
}

func TestUnlockVerdicts(t *testing.T) {
	// Claude: Cloudflare 403 / challenge must NOT report "yes".
	if st, _ := claudeVerdict(403, "", http.Header{"Cf-Mitigated": {"challenge"}}, "Just a moment..."); st == "yes" {
		t.Fatalf("claude 403 challenge falsely reported %q", st)
	}
	if st, _ := claudeVerdict(403, "", http.Header{}, "blocked"); st != "unknown" {
		t.Fatalf("claude 403 = %q, want unknown", st)
	}
	if st, _ := claudeVerdict(302, "https://claude.ai/unavailable", http.Header{}, ""); st != "no" {
		t.Fatalf("claude unavailable redirect = %q, want no", st)
	}
	if st, _ := claudeVerdict(200, "", http.Header{}, "<html>Claude</html>"); st != "yes" {
		t.Fatalf("claude 200 = %q, want yes", st)
	}

	// Disney+: 200 blank SPA must NOT blindly report "yes".
	if st, _, _ := disneyVerdict(200, "", "<html><body><div id='app'></div></body></html>"); st != "unknown" {
		t.Fatalf("disney blank SPA = %q, want unknown", st)
	}
	if st, reg, _ := disneyVerdict(302, "https://www.disneyplus.com/jp-ja", ""); st != "yes" || reg != "JP" {
		t.Fatalf("disney 302 jp = (%q, %q), want (yes, JP)", st, reg)
	}
	if st, _, _ := disneyVerdict(302, "https://www.disneyplus.com/preview", ""); st != "no" {
		t.Fatalf("disney preview = %q, want no", st)
	}

	// YouTube: EU consent redirect must NOT report "yes".
	if st, _, _ := youtubeVerdict(200, "https://consent.youtube.com/m?continue=...", "Before you continue to YouTube"); st != "unknown" {
		t.Fatalf("youtube EU consent = %q, want unknown", st)
	}
	if st, reg, _ := youtubeVerdict(200, "https://www.youtube.com/premium", `{"INNERTUBE_CONTEXT_GL":"DE"}`); st != "yes" || reg != "DE" {
		t.Fatalf("youtube DE = (%q, %q), want (yes, DE)", st, reg)
	}

	// Netflix: must NOT blindly default Region to "US" when Location & body omit country.
	if st, reg, _ := netflixVerdict(200, "", "<html>ok</html>", 200); st != "yes" || reg != "" {
		t.Fatalf("netflix 200 without region = (%q, %q), want (yes, '')", st, reg)
	}
	if st, reg, _ := netflixVerdict(302, "https://www.netflix.com/sg-en/title/81280792", "", 200); st != "yes" || reg != "SG" {
		t.Fatalf("netflix sg redirect = (%q, %q), want (yes, SG)", st, reg)
	}

	// TikTok: blocked regions and captcha handling.
	if st, reg, _ := tiktokVerdict(200, `{"region":"US"}`); st != "yes" || reg != "US" {
		t.Fatalf("tiktok US = (%q, %q), want (yes, US)", st, reg)
	}
	if st, reg, _ := tiktokVerdict(200, `{"region":"CN"}`); st != "no" || reg != "CN" {
		t.Fatalf("tiktok CN = (%q, %q), want (no, CN)", st, reg)
	}
	if st, _, _ := tiktokVerdict(200, `<html>verifycenter captcha</html>`); st != "unknown" {
		t.Fatalf("tiktok captcha = %q, want unknown", st)
	}
	if st, _, _ := tiktokVerdict(403, ""); st != "no" {
		t.Fatalf("tiktok 403 = %q, want no", st)
	}
}

func TestBufferParamsForMem(t *testing.T) {
	// Normal host (1 GiB): standard sizing.
	max1G, rdef1G, wdef1G := bufferParamsForMem("balanced", 1000, 180, 1024<<20)
	if max1G != bufferSize("balanced", 1000, 180) || rdef1G != 131072 || wdef1G != 65536 {
		t.Fatalf("1GiB params = (%d, %d, %d)", max1G, rdef1G, wdef1G)
	}

	// 256 MiB VPS: proportionally scaled down to avoid OOM.
	max256, rdef256, wdef256 := bufferParamsForMem("aggressive", 1000, 180, 256<<20)
	if max256 > 4<<20 || max256 < 1<<20 {
		t.Fatalf("256MiB maxBuf = %d, want in [1MiB, 4MiB]", max256)
	}
	if rdef256 != 65536 || wdef256 != 32768 {
		t.Fatalf("256MiB defaults = (%d, %d), want (65536, 32768)", rdef256, wdef256)
	}

	// 128 MiB VPS: scaled even tighter.
	max128, _, _ := bufferParamsForMem("balanced", 1000, 180, 128<<20)
	if max128 > 2<<20 || max128 < 1<<20 {
		t.Fatalf("128MiB maxBuf = %d, want in [1MiB, 2MiB]", max128)
	}
}

func TestNonInteractiveCmdSetsDebianFrontend(t *testing.T) {
	cmd := nonInteractiveCmd(context.Background(), "apt-get", "install", "-y", "traceroute")
	found := false
	for _, e := range cmd.Env {
		if e == "DEBIAN_FRONTEND=noninteractive" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("apt-get missing DEBIAN_FRONTEND=noninteractive in env")
	}
}
