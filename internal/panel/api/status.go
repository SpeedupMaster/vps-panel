package api

import (
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
	"vpspanel/internal/panel/sub"
)

// Public status page (plan M6). Off by default; when on, /status shows the
// chosen servers' state without login. It is plain server-rendered HTML with
// no IPs, providers, versions or links into the panel, and it does not live
// under admin_path so it never reveals where the panel UI is.

type statusRow struct {
	Name, Flag, Region string
	Online, Blocked    bool
	Uptime             string
	CPU, Mem, Disk     int // percent, -1 = unknown
	Latency            []statusLat
}

type statusLat struct {
	Carrier string
	MS      int // -1 = timeout / no data
	Loss    int
}

var statusTpl = template.Must(template.New("status").Funcs(template.FuncMap{
	"bar": func(label string, v int) map[string]any { return map[string]any{"L": label, "V": v} },
}).Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<meta http-equiv="refresh" content="60">
<title>{{.Title}}</title>
<style>
*{box-sizing:border-box}body{margin:0;background:#f7f8fa;color:#1f2225;font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",sans-serif}
main{max-width:960px;margin:0 auto;padding:28px 16px 40px}
h1{font-size:22px;margin:0 0 4px}.sub{color:#5f6368;font-size:13px;margin:0 0 20px}
.sum{display:inline-block;padding:3px 10px;border-radius:12px;font-size:13px;font-weight:600;margin-bottom:18px}
.sum.ok{background:#e6f4ec;color:#0a5c33}.sum.bad{background:#fbe7ea;color:#8f1c2e}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:12px}
.card{background:#fff;border:1px solid #e4e6ea;border-radius:8px;padding:14px}
.head{display:flex;align-items:center;gap:8px}.name{font-weight:600;flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dot{width:9px;height:9px;border-radius:50%;flex:none}.dot.ok{background:#0f7b45}.dot.off{background:#9aa0a6}.dot.bad{background:#b4233a}
.meta{color:#5f6368;font-size:12px;margin:2px 0 10px}
.bar{display:grid;grid-template-columns:38px 1fr 40px;align-items:center;gap:8px;font-size:12px;color:#3d4248;margin:4px 0}
.track{height:6px;background:#eef0f2;border-radius:3px;overflow:hidden}.fill{height:100%;background:#0f7b45}.fill.warn{background:#b8650a}.fill.bad{background:#b4233a}
.num{text-align:right;font-variant-numeric:tabular-nums}
.lat{display:flex;gap:6px;margin-top:10px;flex-wrap:wrap}.lat span{font-size:12px;padding:2px 8px;border-radius:10px;background:#f4f5f7;color:#3d4248;font-variant-numeric:tabular-nums}
.lat .bad{background:#fbe7ea;color:#8f1c2e}
footer{margin-top:24px;color:#5f6368;font-size:12px}
</style></head><body><main>
<h1>{{.Title}}</h1>
<p class="sub">更新于 {{.Now}} · 每分钟自动刷新</p>
{{if .Rows}}{{if .AllUp}}<span class="sum ok">全部正常</span>{{else}}<span class="sum bad">{{.Down}} 台异常</span>{{end}}{{end}}
<div class="grid">
{{range .Rows}}<section class="card">
 <div class="head"><span>{{.Flag}}</span><span class="name">{{.Name}}</span>
 {{if not .Online}}<span class="dot off" title="离线"></span>{{else if .Blocked}}<span class="dot bad" title="疑似被封"></span>{{else}}<span class="dot ok" title="在线"></span>{{end}}</div>
 <div class="meta">{{if .Region}}{{.Region}} · {{end}}{{if not .Online}}离线{{else if .Blocked}}疑似被封{{else}}运行 {{.Uptime}}{{end}}</div>
 {{if .Online}}
 {{template "bar" (bar "CPU" .CPU)}}{{template "bar" (bar "内存" .Mem)}}{{template "bar" (bar "硬盘" .Disk)}}
 {{if .Latency}}<div class="lat">{{range .Latency}}{{if lt .MS 0}}<span class="bad">{{.Carrier}} 超时</span>{{else}}<span{{if ge .Loss 20}} class="bad"{{end}}>{{.Carrier}} {{.MS}} ms{{if gt .Loss 0}} · {{.Loss}}%{{end}}</span>{{end}}{{end}}</div>{{end}}
 {{end}}
</section>{{else}}<p class="sub">没有可显示的服务器。</p>{{end}}
</div>
{{if .ShowLatency}}<footer>延迟为各服务器到国内三网（电信 / 联通 / 移动）的回程延迟。</footer>{{end}}
</main></body></html>
{{define "bar"}}{{if ge .V 0}}<div class="bar"><span>{{.L}}</span><span class="track"><span class="fill{{if ge .V 90}} bad{{else if ge .V 75}} warn{{end}}" style="width:{{.V}}%;display:block"></span></span><span class="num">{{.V}}%</span></div>{{end}}{{end}}`))

func (a *API) statusPage(c *gin.Context) {
	if !a.P.St.GetBool("status_enabled", false) {
		c.Status(http.StatusNotFound)
		return
	}
	var svs []store.Server
	q := a.P.St.R().Where("status <> ?", "pending").Order("sort, id")
	if spec := strings.TrimSpace(a.P.St.Get("status_servers", "")); spec != "" && spec != "all" {
		var ids []uint
		for _, s := range strings.Split(spec, ",") {
			if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
				ids = append(ids, uint(n))
			}
		}
		q = q.Where("id IN ?", ids)
	}
	q.Find(&svs)

	showLat := a.P.St.GetBool("status_show_latency", true)
	lat := map[uint]map[string][]service.ProbeCell{}
	if showLat {
		var targets []store.ProbeTarget
		a.P.St.R().Find(&targets)
		carrier := map[string]string{}
		for _, t := range targets {
			carrier[t.ID] = t.Carrier
		}
		for _, cell := range a.P.ProbeMatrix("backhaul") {
			cr := carrier[cell.Target]
			if cr == "" {
				continue
			}
			if lat[cell.ServerID] == nil {
				lat[cell.ServerID] = map[string][]service.ProbeCell{}
			}
			lat[cell.ServerID][cr] = append(lat[cell.ServerID][cr], cell)
		}
	}

	rows := make([]statusRow, 0, len(svs))
	down := 0
	for i := range svs {
		sv := &svs[i]
		row := statusRow{Name: sv.Name, Flag: sub.Flag(sv.CountryCode), Region: sv.Region, Online: a.P.Hub.Online(sv.ID), Blocked: sv.Blocked, CPU: -1, Mem: -1, Disk: -1}
		if !row.Online || row.Blocked {
			down++
		}
		if m := a.P.Latest(sv.ID); m != nil && row.Online {
			sys := service.SystemInfo(sv)
			row.CPU = clampPct(m.CPU)
			if sys.MemTotal > 0 {
				row.Mem = clampPct(float64(m.MemUsed) / float64(sys.MemTotal) * 100)
			}
			if sys.DiskTotal > 0 {
				row.Disk = clampPct(float64(m.DiskUsed) / float64(sys.DiskTotal) * 100)
			}
			row.Uptime = humanUptime(time.Duration(m.Uptime) * time.Second)
		}
		for _, cr := range []struct{ id, label string }{{"telecom", "电信"}, {"unicom", "联通"}, {"mobile", "移动"}} {
			cells := lat[sv.ID][cr.id]
			if len(cells) == 0 {
				continue
			}
			var sum, loss float64
			n := 0
			for _, c := range cells {
				loss += c.Loss
				if c.Loss < 100 && c.Avg > 0 {
					sum += c.Avg
					n++
				}
			}
			l := statusLat{Carrier: cr.label, MS: -1, Loss: int(math.Round(loss / float64(len(cells))))}
			if n > 0 {
				l.MS = int(math.Round(sum / float64(n)))
			}
			row.Latency = append(row.Latency, l)
		}
		rows = append(rows, row)
	}

	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(http.StatusOK)
	_ = statusTpl.Execute(c.Writer, gin.H{
		"Title": a.P.St.Get("status_title", "服务状态"), "Now": time.Now().Format("2006-01-02 15:04"),
		"Rows": rows, "Down": down, "AllUp": down == 0, "ShowLatency": showLat,
	})
}

func clampPct(v float64) int {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int(math.Round(v))
}

func humanUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	if days > 0 {
		return strconv.Itoa(days) + " 天"
	}
	if h := int(d.Hours()); h > 0 {
		return strconv.Itoa(h) + " 小时"
	}
	return strconv.Itoa(int(d.Minutes())) + " 分钟"
}
