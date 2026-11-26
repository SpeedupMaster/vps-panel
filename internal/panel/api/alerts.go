package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

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
	ok(c, a.P.ListAlertRules())
}

func (a *API) saveRule(c *gin.Context) {
	var r store.AlertRule
	if err := c.ShouldBindJSON(&r); err != nil {
		bad(c, err)
		return
	}
	r.ID = idParam(c)
	if err := a.P.SaveAlertRule(&r, c.ClientIP()); err != nil {
		bad(c, err)
		return
	}
	ok(c, r)
}

func (a *API) deleteRule(c *gin.Context) {
	if err := a.P.DeleteAlertRule(idParam(c), c.ClientIP()); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{})
}

// ---------- events ----------

func (a *API) listEvents(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	evs, firing := a.P.ListAlertEvents(service.AlertEventFilter{
		Status:   c.Query("status"),
		Kind:     c.Query("kind"),
		ServerID: c.Query("server_id"),
		Severity: c.Query("severity"),
		Limit:    limit,
	})
	type view struct {
		store.AlertEvent
		ServerName string `json:"server_name"`
		KindLabel  string `json:"kind_label"`
	}
	out := make([]view, 0, len(evs))
	for _, e := range evs {
		out = append(out, view{e, a.P.ServerName(e.ServerID), service.KindLabels[e.Kind]})
	}
	ok(c, gin.H{"items": out, "firing": firing})
}

func (a *API) ackEvent(c *gin.Context) {
	if err := a.P.AckAlertEvent(idParam(c)); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{})
}

func (a *API) ackAll(c *gin.Context) {
	if err := a.P.AckAllAlertEvents(); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{})
}

func (a *API) silenceEvent(c *gin.Context) {
	var in struct {
		Minutes int `json:"minutes"`
	}
	_ = c.ShouldBindJSON(&in)
	to, err := a.P.SilenceAlertEvent(idParam(c), in.Minutes)
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"silenced_to": to})
}

// ---------- channels ----------

var secretKeys = map[string]bool{"bot_token": true, "password": true, "secret": true, "device_key": true, "webhook": true}

func (a *API) listChannels(c *gin.Context) {
	chs := a.P.ListNotifyChannels()
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
	if err := a.P.DeleteNotifyChannel(idParam(c)); err != nil {
		bad(c, err)
		return
	}
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
	ok(c, gin.H{"items": a.P.ListProbeTargets(c.Query("source")), "synced_at": a.P.St.Get("zstatic_synced_at", "")})
}

func validTarget(t *store.ProbeTarget) error { return service.ValidateProbeTarget(t) }

func (a *API) createTarget(c *gin.Context) {
	var t store.ProbeTarget
	if err := c.ShouldBindJSON(&t); err != nil {
		bad(c, err)
		return
	}
	if err := a.P.CreateProbeTarget(&t); err != nil {
		bad(c, err)
		return
	}
	ok(c, t)
}

func (a *API) updateTarget(c *gin.Context) {
	var in store.ProbeTarget
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	cur, err := a.P.UpdateProbeTarget(c.Param("tid"), in)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			fail(c, http.StatusNotFound, "目标不存在")
			return
		}
		bad(c, err)
		return
	}
	ok(c, cur)
}

func (a *API) deleteTarget(c *gin.Context) {
	if err := a.P.DeleteProbeTarget(c.Param("tid")); err != nil {
		bad(c, err)
		return
	}
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
	if err := a.P.SetProbeTargetsBulk(in.Enabled); err != nil {
		bad(c, err)
		return
	}
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
	var list []string
	for _, t := range strings.Split(c.Query("targets"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			list = append(list, t)
			if len(list) >= 60 {
				break
			}
		}
	}
	ok(c, a.P.ProbeSeriesMany(uint(sid), list, rng))
}
