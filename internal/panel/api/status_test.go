package api

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestStatusTemplateRenders(t *testing.T) {
	rows := []statusRow{
		{Name: "HK-1", Flag: "🇭🇰", Region: "香港", Online: true, Uptime: "3 天", CPU: 12, Mem: 80, Disk: 95,
			Latency: []statusLat{{Carrier: "电信", MS: 38}, {Carrier: "联通", MS: -1, Loss: 100}, {Carrier: "移动", MS: 61, Loss: 25}}},
		{Name: "JP-1", Online: false, CPU: -1, Mem: -1, Disk: -1},
		{Name: "US-1", Online: true, Blocked: true, CPU: 5, Mem: -1, Disk: -1},
	}
	var buf bytes.Buffer
	err := statusTpl.Execute(&buf, map[string]any{"Title": "<b>状态</b>", "Now": "2026-11-16 12:00", "Rows": rows, "Down": 2, "AllUp": false, "ShowLatency": true})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"&lt;b&gt;状态&lt;/b&gt;", "2 台异常", "HK-1", "电信 38 ms", "联通 超时", "移动 61 ms · 25%", "fill bad", "fill warn", "离线", "疑似被封"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Count(out, `class="bar"`) != 4 { // HK: 3 bars, US-1: CPU only
		t.Errorf("bars = %d, want 4", strings.Count(out, `class="bar"`))
	}
}

func TestHumanUptime(t *testing.T) {
	cases := map[int64]string{59: "0 分钟", 3600 * 5: "5 小时", 86400*2 + 10: "2 天"}
	for sec, want := range cases {
		if got := humanUptime(timeSeconds(sec)); got != want {
			t.Errorf("%d: got %q want %q", sec, got, want)
		}
	}
}

func timeSeconds(s int64) time.Duration { return time.Duration(s) * time.Second }
