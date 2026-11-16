package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/store"
)

func (a *API) installScript(c *gin.Context) {
	c.Header("Content-Type", "text/x-shellscript; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Writer.Write(a.installScriptBody())
}

func (a *API) listTasks(c *gin.Context) {
	q := a.P.St.DB.Order("id desc").Limit(200)
	if s := c.Query("server"); s != "" {
		q = q.Where("server_id = ?", s)
	}
	if t := c.Query("type"); t != "" {
		q = q.Where("type IN ?", strings.Split(t, ","))
	}
	if b := c.Query("batch"); b != "" {
		q = q.Where("batch_id = ?", b)
	}
	var ts []store.Task
	q.Omit("output").Find(&ts)
	ok(c, ts)
}

func (a *API) getTask(c *gin.Context) {
	var t store.Task
	if err := a.P.St.DB.First(&t, c.Param("id")).Error; err != nil {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	ok(c, t)
}

// streamTask sends task output chunks as server-sent events.
func (a *API) streamTask(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var t store.Task
	if err := a.P.St.DB.First(&t, id).Error; err != nil {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	c.SSEvent("output", t.Output)
	c.Writer.Flush()
	if t.FinishedAt != nil {
		c.SSEvent("done", t.Status)
		return
	}
	ch := a.P.WatchTask(id)
	defer a.P.UnwatchTask(id, ch)
	c.Stream(func(w io.Writer) bool {
		select {
		case r, okk := <-ch:
			if !okk {
				c.SSEvent("done", "finished")
				return false
			}
			if r.Output != "" {
				c.SSEvent("output", r.Output)
			}
			if r.Finished {
				c.SSEvent("done", r.Status)
				return false
			}
			return true
		case <-time.After(25 * time.Second):
			c.SSEvent("ping", "")
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

// events streams bus events (metrics, status, tasks, alerts) to the UI.
func (a *API) events(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	ch := a.P.Bus.Subscribe()
	defer a.P.Bus.Unsubscribe(ch)
	c.Stream(func(w io.Writer) bool {
		select {
		case e := <-ch:
			b, _ := json.Marshal(e.Data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
			return true
		case <-time.After(25 * time.Second):
			fmt.Fprint(w, ": ping\n\n")
			return true
		case <-c.Request.Context().Done():
			return false
		}
	})
}

func (a *API) audit(c *gin.Context) {
	q := a.P.St.DB.Order("id desc")
	if t := c.Query("type"); t != "" && t != "all" {
		q = q.Where("type = ?", t)
	}
	if c.Query("format") == "csv" {
		var logs []store.AuditLog
		q.Limit(20000).Find(&logs)
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", "attachment; filename=audit.csv")
		c.Writer.Write([]byte("\xEF\xBB\xBF"))
		w := csv.NewWriter(c.Writer)
		_ = w.Write([]string{"time", "type", "action", "target", "detail", "ip"})
		for _, l := range logs {
			_ = w.Write([]string{l.TS.Format(time.RFC3339), l.Type, l.Action, l.Target, l.Detail, l.IP})
		}
		w.Flush()
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	var logs []store.AuditLog
	q.Limit(limit).Offset(offset).Find(&logs)
	ok(c, logs)
}

// Settings the UI may read and write. Secrets never go through here.
var settingKeys = []string{
	"panel_url", "panel_urls_backup", "sub_url", "admin_path", "allow_ips", "currency",
	"agent_report_interval", "probe_interval", "offline_after_sec",
	"retention_metrics_1m_days", "retention_metrics_1h_days", "retention_probe_days", "retention_probe_1h_days",
	"backup_enabled", "backup_time", "backup_keep", "backup_target",
	"sub_name_template", "sub_hide_unhealthy", "sub_update_interval", "sub_userinfo_server",
	"alert_quiet_enabled", "alert_quiet_from", "alert_quiet_to", "alert_merge", "alert_recover",
	"xray_auto_check", "geo_source", "fx_rates",
}

func (a *API) getSettings(c *gin.Context) {
	out := gin.H{}
	for _, k := range settingKeys {
		out[k] = a.P.St.Get(k, "")
	}
	ok(c, out)
}

func (a *API) putSettings(c *gin.Context) {
	var in map[string]any
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	allowed := map[string]bool{}
	for _, k := range settingKeys {
		allowed[k] = true
	}
	var changed []string
	for k, v := range in {
		if !allowed[k] {
			continue
		}
		s := fmt.Sprint(v)
		if b, isBool := v.(bool); isBool {
			s = map[bool]string{true: "1", false: "0"}[b]
		}
		if k == "allow_ips" && s != "" {
			ip := c.ClientIP()
			if !ipListed(s, ip) {
				bad(c, fmt.Errorf("允许列表里没有你当前的 IP（%s），保存后会把自己锁在外面", ip))
				return
			}
		}
		_ = a.P.St.Set(k, s)
		changed = append(changed, k)
	}
	a.P.Audit("system", "settings", strings.Join(changed, ","), "", c.ClientIP())
	a.P.OnSettingsChanged(changed)
	ok(c, gin.H{"ok": true})
}

func ipListed(list, ip string) bool { return auth.IPAllowed(list, ip) }
