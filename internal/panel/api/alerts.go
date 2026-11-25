package api

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/notify"
	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
)

func (a *API) mountAlerts(g *gin.RouterGroup) {
	g.GET("/alerts/rules", a.listRules)
	g.POST("/alerts/rules", a.saveRule)
	g.PUT("/alerts/rules/:id", a.saveRule)
	g.DELETE("/alerts/rules/:id", a.deleteRule)
	g.GET("/alerts/kinds", func(c *gin.Context) { ok(c, service.KindLabels) })
	g.GET("/alerts/events", a.listEvents)
	g.POST("/alerts/events/:id/ack", a.ackEvent)
	g.POST("/alerts/events/:id/silence", a.silenceEvent)
	g.POST("/alerts/events/ack-all", a.ackAll)

	g.GET("/channels", a.listChannels)
	g.GET("/channels/fields", func(c *gin.Context) { ok(c, notify.Fields) })
	g.POST("/channels", a.saveChannel)
	g.PUT("/channels/:id", a.saveChannel)
	g.DELETE("/channels/:id", a.deleteChannel)
	g.POST("/channels/:id/test", a.testChannel)

	g.GET("/probes/targets", a.listTargets)
	g.POST("/probes/targets", a.createTarget)
	g.PUT("/probes/targets/:tid", a.updateTarget)
	g.DELETE("/probes/targets/:tid", a.deleteTarget)
	g.POST("/probes/targets/bulk", a.bulkTargets)
	g.POST("/probes/sync", a.syncTargets)
	g.GET("/probes/matrix", func(c *gin.Context) { ok(c, a.P.ProbeMatrix(c.Query("kind"))) })
	g.GET("/probes/series", a.probeSeries)
}

// ---------- rules ----------

func (a *API) listRules(c *gin.Context) {
	var rs []store.AlertRule
	a.P.St.DB.Order("id").Find(&rs)
	ok(c, rs)
}

func (a *API) saveRule(c *gin.Context) {
	var r store.AlertRule
	if err := c.ShouldBindJSON(&r); err != nil {
		bad(c, err)
		return
	}
	r.ID = idParam(c)
	if r.Name == "" || service.KindLabels[r.Kind] == "" {
		bad(c, errors.New("名称或类型无效"))
		return
	}
	if r.Severity == "" {
		r.Severity = "warning"
	}
	if r.Scope == "" {
		r.Scope = "all"
	}
	if err := a.P.St.DB.Save(&r).Error; err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("alert", "save_rule", r.Name, "", c.ClientIP())
	ok(c, r)
}

func (a *API) deleteRule(c *gin.Context) {
	a.P.St.DB.Delete(&store.AlertRule{}, idParam(c))
	ok(c, gin.H{})
}

// ---------- events ----------

func (a *API) listEvents(c *gin.Context) {
	q := a.P.St.R().Order("id desc")
	if s := c.Query("status"); s != "" && s != "all" {
		q = q.Where("status = ?", s)
	}
	if k := c.Query("kind"); k != "" {
		q = q.Where("kind = ?", k)
	}
	if sid := c.Query("server_id"); sid != "" {
		q = q.Where("server_id = ?", sid)
	}
	if sev := c.Query("severity"); sev != "" {
		q = q.Where("severity = ?", sev)
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	var evs []store.AlertEvent
	q.Limit(limit).Find(&evs)
	type view struct {
		store.AlertEvent
		ServerName string `json:"server_name"`
		KindLabel  string `json:"kind_label"`
	}
	out := make([]view, 0, len(evs))
	for _, e := range evs {
		out = append(out, view{e, a.P.ServerName(e.ServerID), service.KindLabels[e.Kind]})
	}
	var firing int64
	a.P.St.R().Model(&store.AlertEvent{}).Where("status = ? AND acked = ?", "firing", false).Count(&firing)
	ok(c, gin.H{"items": out, "firing": firing})
}

func (a *API) ackEvent(c *gin.Context) {
	a.P.St.DB.Model(&store.AlertEvent{}).Where("id = ?", idParam(c)).Update("acked", true)
	a.P.Bus.Publish("alert", gin.H{"id": idParam(c), "acked": true})
	ok(c, gin.H{})
}

func (a *API) ackAll(c *gin.Context) {
	a.P.St.DB.Model(&store.AlertEvent{}).Where("status IN ? AND acked = ?", []string{"firing", "info"}, false).Update("acked", true)
	ok(c, gin.H{})
}

func (a *API) silenceEvent(c *gin.Context) {
	var in struct {
		Minutes int `json:"minutes"`
	}
	_ = c.ShouldBindJSON(&in)
	if in.Minutes <= 0 {
		in.Minutes = 60
	}
	to := time.Now().Add(time.Duration(in.Minutes) * time.Minute)
	a.P.St.DB.Model(&store.AlertEvent{}).Where("id = ?", idParam(c)).Update("silenced_to", &to)
	ok(c, gin.H{"silenced_to": to})
}

// ---------- channels ----------

var secretKeys = map[string]bool{"bot_token": true, "password": true, "secret": true, "device_key": true, "webhook": true}

func (a *API) listChannels(c *gin.Context) {
	var chs []store.NotifyChannel
	a.P.St.DB.Order("id").Find(&chs)
	type view struct {
		store.NotifyChannel
		Config map[string]string `json:"config"`
	}
	out := make([]view, 0, len(chs))
	for i := range chs {
		cfg, _ := a.P.ChannelConfig(&chs[i])
		for k, v := range cfg {
			if secretKeys[k] && v != "" {
				cfg[k] = "••••••"
			}
		}
		out = append(out, view{chs[i], cfg})
	}
	ok(c, out)
}

func (a *API) saveChannel(c *gin.Context) {
	var in struct {
		store.NotifyChannel
		Config map[string]string `json:"config"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	ch := in.NotifyChannel
	ch.ID = idParam(c)
	if ch.Name == "" {
		ch.Name = ch.Type
	}
	if in.Config == nil {
		in.Config = map[string]string{}
	}
	if err := a.P.SaveChannel(&ch, in.Config); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("alert", "save_channel", ch.Name, ch.Type, c.ClientIP())
	ok(c, ch)
}

func (a *API) deleteChannel(c *gin.Context) {
	a.P.St.DB.Delete(&store.NotifyChannel{}, idParam(c))
	ok(c, gin.H{})
}

func (a *API) testChannel(c *gin.Context) {
	if err := a.P.TestChannel(idParam(c)); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

// ---------- probe targets ----------

func (a *API) listTargets(c *gin.Context) {
	var ts []store.ProbeTarget
	q := a.P.St.DB.Order("source, province, carrier, id")
	if s := c.Query("source"); s != "" {
		q = q.Where("source = ?", s)
	}
	q.Find(&ts)
	ok(c, gin.H{"items": ts, "synced_at": a.P.St.Get("zstatic_synced_at", "")})
}

func validTarget(t *store.ProbeTarget) error {
	t.Name = strings.TrimSpace(t.Name)
	t.Host = strings.TrimSpace(t.Host)
	if t.Name == "" || t.Host == "" {
		return errors.New("名称和地址必填")
	}
	if t.Type == "" {
		t.Type = "tcp"
	}
	if t.Type != "tcp" && t.Type != "icmp" {
		return errors.New("类型只能是 tcp 或 icmp")
	}
	if t.Type == "tcp" && (t.Port <= 0 || t.Port > 65535) {
		return errors.New("TCP 端口无效")
	}
	if strings.ContainsAny(t.Host, " \t\r\n/\\") {
		return errors.New("地址无效")
	}
	lower := strings.ToLower(t.Host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return errors.New("不能探测本地或内网地址")
	}
	if ip := net.ParseIP(t.Host); ip != nil {
		if notify.IsBlockedIP(ip) {
			return errors.New("不能探测本地或内网地址")
		}
		return nil
	}
	if !strings.Contains(t.Host, ".") {
		return errors.New("地址无效")
	}
	return nil
}

func (a *API) createTarget(c *gin.Context) {
	var t store.ProbeTarget
	if err := c.ShouldBindJSON(&t); err != nil {
		bad(c, err)
		return
	}
	if err := validTarget(&t); err != nil {
		bad(c, err)
		return
	}
	t.ID = fmt.Sprintf("cu-%d", time.Now().UnixNano()%1e9)
	t.Source = "custom"
	t.Enabled = true
	if err := a.P.St.DB.Create(&t).Error; err != nil {
		bad(c, err)
		return
	}
	a.P.PushProbeTasksAll()
	ok(c, t)
}

func (a *API) updateTarget(c *gin.Context) {
	var cur store.ProbeTarget
	if err := a.P.St.DB.First(&cur, "id = ?", c.Param("tid")).Error; err != nil {
		fail(c, 404, "目标不存在")
		return
	}
	var in store.ProbeTarget
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if cur.Source == "custom" {
		in.ID, in.Source = cur.ID, cur.Source
		if err := validTarget(&in); err != nil {
			bad(c, err)
			return
		}
		cur = in
	} else {
		cur.Enabled = in.Enabled // zstatic targets: only toggle
	}
	a.P.St.DB.Save(&cur)
	a.P.PushProbeTasksAll()
	ok(c, cur)
}

func (a *API) deleteTarget(c *gin.Context) {
	tid := c.Param("tid")
	a.P.St.DB.Where("id = ? AND source = ?", tid, "custom").Delete(&store.ProbeTarget{})
	a.P.ForgetProbeTarget(tid)
	a.P.PushProbeTasksAll()
	ok(c, gin.H{})
}

// bulkTargets enables exactly the given ids (the target picker's "save").
func (a *API) bulkTargets(c *gin.Context) {
	var in struct {
		Enabled []string `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if len(in.Enabled) > 60 {
		bad(c, errors.New("最多同时启用 60 个目标"))
		return
	}
	db := a.P.St.DB
	db.Model(&store.ProbeTarget{}).Where("1 = 1").Update("enabled", false)
	if len(in.Enabled) > 0 {
		db.Model(&store.ProbeTarget{}).Where("id IN ?", in.Enabled).Update("enabled", true)
	}
	a.P.PushProbeTasksAll()
	ok(c, gin.H{"enabled": len(in.Enabled)})
}

func (a *API) syncTargets(c *gin.Context) {
	n, err := a.P.SyncZstatic()
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"count": n, "synced_at": a.P.St.Get("zstatic_synced_at", "")})
}

func (a *API) probeSeries(c *gin.Context) {
	sid, _ := strconv.ParseUint(c.Query("server_id"), 10, 64)
	rng, _ := strconv.ParseInt(c.DefaultQuery("range", "86400"), 10, 64)
	if rng <= 0 {
		rng = 86400
	} else if rng > 400*86400 {
		rng = 400 * 86400
	}
	targets := strings.Split(c.Query("targets"), ",")
	out := map[string]any{}
	count := 0
	for _, t := range targets {
		if t = strings.TrimSpace(t); t != "" {
			if count >= 60 {
				break
			}
			out[t] = a.P.ProbeSeries(uint(sid), t, rng)
			count++
		}
	}
	ok(c, out)
}
