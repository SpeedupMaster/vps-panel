package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
)

type serverView struct {
	*store.Server
	System   any           `json:"system"`
	Switches any           `json:"switches"`
	Latest   any           `json:"latest"`
	Online   bool          `json:"online"`
	Roles    []string      `json:"roles"`
	Usage    service.Usage `json:"usage"`
	Monthly  float64       `json:"monthly_cost"`
}

func (a *API) viewWith(sv *store.Server, roles []string, usage service.Usage) serverView {
	return serverView{
		Server: sv, System: service.SystemInfo(sv), Switches: service.Switches(sv),
		Latest: a.P.Latest(sv.ID), Online: a.P.Hub.Online(sv.ID), Roles: roles,
		Usage: usage, Monthly: service.MonthlyCost(sv),
	}
}

func (a *API) view(sv *store.Server) serverView {
	return a.viewWith(sv, a.P.Roles(sv), a.P.CycleUsage(sv))
}

func (a *API) listServers(c *gin.Context) {
	var svs []store.Server
	a.P.St.R().Order("sort, id").Find(&svs)
	rolesBy := a.P.RolesMap(svs)
	usageBy := a.P.CycleUsageMap(svs)
	out := make([]serverView, 0, len(svs))
	for i := range svs {
		sv := &svs[i]
		out = append(out, a.viewWith(sv, rolesBy[sv.ID], usageBy[sv.ID]))
	}
	ok(c, out)
}

func (a *API) getServer(c *gin.Context) {
	var sv store.Server
	if err := a.P.St.R().First(&sv, idParam(c)).Error; err != nil {
		fail(c, http.StatusNotFound, "服务器不存在")
		return
	}
	ok(c, a.view(&sv))
}

func (a *API) createServer(c *gin.Context) {
	var in service.ServerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	sv, token, err := a.P.CreateServer(in)
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "create", sv.Name, sv.Mode, c.ClientIP())
	ok(c, gin.H{"server": sv, "token": token, "command": a.P.InstallCommand(token, sv.Mode), "expires_at": sv.InstallTokenExp, "install_token_exp": sv.InstallTokenExp})
}

func (a *API) updateServer(c *gin.Context) {
	var in service.ServerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if err := a.P.UpdateServer(idParam(c), in); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "update", in.Name, "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) deleteServer(c *gin.Context) {
	if !a.confirm(c) {
		return
	}
	id := idParam(c)
	name := a.P.ServerName(id)
	if err := a.P.DeleteServer(id); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "delete", name, "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) newToken(c *gin.Context) {
	var sv store.Server
	if err := a.P.St.DB.First(&sv, idParam(c)).Error; err != nil {
		fail(c, http.StatusNotFound, "服务器不存在")
		return
	}
	token, err := a.P.NewInstallToken(sv.ID)
	if err != nil {
		bad(c, err)
		return
	}
	a.P.St.DB.Select("install_token_exp").First(&sv, sv.ID)
	a.P.Audit("server", "new_install_token", sv.Name, "", c.ClientIP())
	ok(c, gin.H{"token": token, "command": a.P.InstallCommand(token, sv.Mode), "expires_at": sv.InstallTokenExp, "install_token_exp": sv.InstallTokenExp})
}

func (a *API) serverMetrics(c *gin.Context) {
	rng, _ := strconv.ParseInt(c.DefaultQuery("range", "3600"), 10, 64)
	if rng <= 0 || rng > 400*86400 {
		rng = 3600
	}
	ok(c, a.P.MetricSeries(idParam(c), rng))
}

func (a *API) overview(c *gin.Context) {
	db := a.P.St.R()
	var svs []store.Server
	db.Order("sort, id").Find(&svs)
	usageBy := a.P.CycleUsageMap(svs)
	online, nodes, probes := 0, 0, 0
	var monthly float64
	var upMonth, downMonth uint64
	type expiring struct {
		ID   uint      `json:"id"`
		Name string    `json:"name"`
		At   time.Time `json:"at"`
		Days int       `json:"days"`
	}
	var exp []expiring
	type share struct {
		ID    uint   `json:"id"`
		Name  string `json:"name"`
		Used  uint64 `json:"used"`
		Quota int64  `json:"quota"`
	}
	var shares []share
	for i := range svs {
		sv := &svs[i]
		if a.P.Hub.Online(sv.ID) {
			online++
		}
		if sv.Mode == "node" {
			nodes++
		} else {
			probes++
		}
		monthly += a.P.ToCurrency(service.MonthlyCost(sv), sv.Currency)
		u := usageBy[sv.ID]
		upMonth += u.Up
		downMonth += u.Down
		shares = append(shares, share{sv.ID, sv.Name, u.Counted, sv.QuotaBytes})
		if sv.ExpireAt != nil {
			d := int(time.Until(*sv.ExpireAt).Hours() / 24)
			if d <= 30 {
				exp = append(exp, expiring{sv.ID, sv.Name, *sv.ExpireAt, d})
			}
		}
	}
	sort.Slice(shares, func(i, j int) bool { return shares[i].Used > shares[j].Used })
	sort.Slice(exp, func(i, j int) bool { return exp[i].At.Before(exp[j].At) })
	var firing int64
	db.Model(&store.AlertEvent{}).Where("status = ?", "firing").Count(&firing)
	var recent []store.AlertEvent
	db.Order("started_at desc").Limit(6).Find(&recent)
	ok(c, gin.H{
		"total": len(svs), "online": online, "nodes": nodes, "probes": probes,
		"monthly_cost": monthly, "currency": a.P.St.Get("currency", "USD"),
		"traffic_up": upMonth, "traffic_down": downMonth,
		"expiring": exp, "alerts_firing": firing, "recent_alerts": recent, "traffic_share": shares,
	})
}

// serverTraffic: NIC daily series plus this cycle's per-route and landing-user totals.
func (a *API) serverTraffic(c *gin.Context) {
	var sv store.Server
	if err := a.P.St.R().First(&sv, idParam(c)).Error; err != nil {
		fail(c, http.StatusNotFound, "服务器不存在")
		return
	}
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	if days <= 0 || days > 400 {
		days = 30
	}
	sid := strconv.FormatUint(uint64(sv.ID), 10)
	now := time.Now()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	usage := a.P.CycleUsage(&sv)
	type item struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
		Up   uint64 `json:"up"`
		Down uint64 `json:"down"`
	}
	var routes []store.Route
	a.P.St.R().Where("entry_server_id = ?", sv.ID).Order("sort, id").Find(&routes)
	rids := make([]string, len(routes))
	for i, r := range routes {
		rids[i] = strconv.FormatUint(uint64(r.ID), 10)
	}
	sums := a.P.SumMany("route", rids, usage.CycleStart)
	rs := make([]item, 0, len(routes))
	for i, r := range routes {
		t := sums[rids[i]]
		rs = append(rs, item{r.ID, r.Name, t[0], t[1]})
	}
	ok(c, gin.H{
		"daily":  a.P.TrafficSeries("server", sid, since, true),
		"hourly": a.P.TrafficSeries("server", sid, now.Add(-48*time.Hour), false),
		"routes": rs, "usage": usage,
	})
}
