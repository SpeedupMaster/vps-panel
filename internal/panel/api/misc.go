package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/service"
)

func (a *API) installScript(c *gin.Context) {
	c.Header("Content-Type", "text/x-shellscript; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Writer.Write(a.installScriptBody())
}

func (a *API) listTasks(c *gin.Context) {
	ok(c, a.P.ListTasks(c.Query("server"), c.Query("type"), c.Query("batch"), 200))
}

func (a *API) getTask(c *gin.Context) {
	t, err := a.P.GetTask(c.Param("id"))
	if err != nil {
		fail(c, http.StatusNotFound, "任务不存在")
		return
	}
	ok(c, t)
}

// streamTask sends task output chunks as server-sent events.
func (a *API) streamTask(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	t, err := a.P.GetTask(id)
	if err != nil {
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
	// Send headers now so the browser's EventSource opens immediately
	// instead of after the first event or keep-alive.
	c.Status(http.StatusOK)
	fmt.Fprint(c.Writer, ": hello\n\n")
	c.Writer.Flush()
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
	typ := c.Query("type")
	if c.Query("format") == "csv" {
		logs := a.P.AuditLogs(typ, 20000, 0)
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", "attachment; filename=audit.csv")
		c.Writer.Write([]byte("\xEF\xBB\xBF"))
		w := csv.NewWriter(c.Writer)
		_ = w.Write([]string{"time", "type", "action", "target", "detail", "ip"})
		for _, l := range logs {
			_ = w.Write([]string{
				csvSafeCell(l.TS.Format(time.RFC3339)),
				csvSafeCell(l.Type),
				csvSafeCell(l.Action),
				csvSafeCell(l.Target),
				csvSafeCell(l.Detail),
				csvSafeCell(l.IP),
			})
		}
		w.Flush()
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 {
		limit = 100
	} else if limit > 500 {
		limit = 500
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		offset = 0
	}
	ok(c, a.P.AuditLogs(typ, limit, offset))
}

// Settings the UI may read and write. Secrets never go through here.
var settingKeys = []string{
	// panel & access
	"panel_url", "panel_urls_backup", "sub_url", "admin_path", "allow_ips", "currency", "fx_rates",
	// agents & probes
	"agent_report_interval", "offline_after_sec", "probe_interval", "probe_count", "probe_peer", "block_rounds",
	// retention
	"retention_metrics_1m_days", "retention_metrics_1h_days", "retention_probe_raw_days", "retention_probe_1h_days", "retention_traffic_days", "retention_tool_results_days", "config_versions_keep",
	// xray
	"auto_install_xray", "drift_autofix", "reality_default_target", "landing_firewall", "xray_access_log", "xray_block_bt",
	"xray_probe_url", "xray_pinned_version", "xray_relay", "geo_source",
	// subscriptions
	"sub_name_tpl", "sub_profile_name", "sub_update_hours", "sub_userinfo", "sub_exclude_blocked", "sub_exclude_offline", "sub_routes",
	// alerts
	"alert_quiet", "alert_recovery", "traffic_spike_factor", "traffic_spike_min_gb",
	// public status page
	"status_enabled", "status_title", "status_servers", "status_show_latency",
	// agent update signing
	"sign_pubkey",
	// backups
	"backup_enabled", "backup_time", "backup_keep", "backup_target", "backup_s3_endpoint", "backup_s3_bucket", "backup_s3_region",
	"backup_s3_access_key", "backup_s3_prefix", "backup_webdav_url", "backup_webdav_user", "backup_tg_channel",
}

func (a *API) getSettings(c *gin.Context) {
	out := gin.H{}
	for _, k := range settingKeys {
		out[k] = a.P.St.Get(k, "")
	}
	for _, k := range service.SecretSettings {
		out[k] = ""
		if a.P.St.Get(k, "") != "" {
			out[k] = service.SecretMask
		}
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
	if v, has := in["sign_pubkey"]; has && fmt.Sprint(v) != "" {
		if _, err := service.ParseEd25519Public(fmt.Sprint(v)); err != nil {
			bad(c, errors.New("签名公钥无效："+err.Error()))
			return
		}
	}
	toSet := map[string]string{}
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
		if k == "admin_path" && strings.Trim(strings.TrimSpace(s), "/") != "" {
			seg, _, _ := strings.Cut(strings.Trim(strings.TrimSpace(s), "/"), "/")
			if a.P.HasSubTokenFold(seg) {
				bad(c, errors.New("后台路径与现有订阅后缀冲突，请更换后台路径或先修改对应设备的订阅后缀"))
				return
			}
		}
		if k == "panel_url" || k == "sub_url" {
			if err := validateSettingURL(s); err != nil {
				bad(c, fmt.Errorf("%s 无效：%w", k, err))
				return
			}
		}
		if k == "panel_urls_backup" && strings.TrimSpace(s) != "" {
			for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if err := validateSettingURL(line); err != nil {
					bad(c, fmt.Errorf("panel_urls_backup 无效：%w", err))
					return
				}
			}
		}
		toSet[k] = s
	}
	for _, k := range service.SecretSettings {
		if v, has := in[k]; has {
			if err := a.P.SetSecretSetting(k, fmt.Sprint(v)); err != nil {
				bad(c, err)
				return
			}
			changed = append(changed, k)
		}
	}
	for k, s := range toSet {
		_ = a.P.St.Set(k, s)
		changed = append(changed, k)
	}
	a.P.Audit("system", "settings", strings.Join(changed, ","), "", c.ClientIP())
	a.P.OnSettingsChanged(changed)
	ok(c, gin.H{"ok": true})
}

func ipListed(list, ip string) bool { return auth.IPAllowed(list, ip) }

func csvSafeCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

var urlHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`)

func validateSettingURL(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return errors.New("包含非法控制字符")
		}
	}
	if strings.ContainsAny(s, " \t\r\n'\"`$;|&<>()\\{}*?#") {
		return errors.New("包含非法字符或空白")
	}
	u, err := url.Parse(s)
	if err != nil {
		return errors.New("URL 格式错误")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("必须以 http:// 或 https:// 开头")
	}
	if u.User != nil {
		return errors.New("不允许包含用户名或密码")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("缺少主机名")
	}
	if net.ParseIP(host) == nil && !urlHostRe.MatchString(host) {
		return errors.New("主机名无效")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return errors.New("端口无效")
		}
	}
	return nil
}
