package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
	"vpspanel/internal/panel/sub"
)

func monthStart() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
}

func prettyJSON(b []byte) []byte {
	var buf bytes.Buffer
	if json.Indent(&buf, b, "", "  ") != nil {
		return b
	}
	return buf.Bytes()
}

var secretFields = regexp.MustCompile(`("(?:privateKey|password)":\s*")[^"]*(")`)

// redactConfig hides keys in configs shown in the UI.
func redactConfig(s string) string {
	return secretFields.ReplaceAllString(s, "${1}••••••${2}")
}

func subDefaultMihomo() string  { return sub.DefaultMihomo }
func subDefaultSingBox() string { return sub.DefaultSingBox }

func (a *API) mountNodes(r *gin.Engine, g *gin.RouterGroup) {
	g.GET("/servers/:id/xray", a.getXray)
	g.PUT("/servers/:id/reality", a.putReality)
	g.PUT("/servers/:id/landing", a.putLanding)
	g.POST("/servers/:id/upgrade", a.upgradeNode)
	g.POST("/servers/:id/downgrade", a.downgradeNode)
	g.POST("/servers/:id/deploy", a.deployNode)
	g.GET("/servers/:id/configs", a.listConfigs)
	g.GET("/servers/:id/configs/:vid", a.getConfig)
	g.POST("/servers/:id/configs/:vid/rollback", a.rollbackConfig)
	g.GET("/servers/:id/preview-config", a.previewConfig)

	g.GET("/routes", a.listRoutes)
	g.POST("/routes", a.createRoute)
	g.PUT("/routes/:id", a.updateRoute)
	g.DELETE("/routes/:id", a.deleteRoute)
	g.POST("/routes/:id/toggle", a.toggleRoute)
	g.POST("/routes/:id/regen-uuid", a.regenRoute)
	g.GET("/topology", a.topology)

	g.GET("/sub/tokens", a.listSubTokens)
	g.POST("/sub/tokens", a.createSubToken)
	g.PUT("/sub/tokens/:id", a.updateSubToken)
	g.POST("/sub/tokens/:id/reset", a.resetSubToken)
	g.DELETE("/sub/tokens/:id", a.deleteSubToken)
	g.GET("/sub/preview", a.previewSub)
	g.GET("/sub/logs", a.subLogs)
	g.GET("/sub/templates", a.listTemplates)
	g.POST("/sub/templates", a.saveTemplate)
	g.PUT("/sub/templates/:id", a.saveTemplate)
	g.DELETE("/sub/templates/:id", a.deleteTemplate)
	g.GET("/sub/templates/default", a.defaultTemplate)
	g.POST("/sub/rotate-all", a.rotateAll)

	r.GET("/s/:token", a.serveSub)
}

// ---------- xray / inbounds ----------

func (a *API) getXray(c *gin.Context) {
	id := idParam(c)
	var sv store.Server
	if err := a.P.St.DB.First(&sv, id).Error; err != nil {
		fail(c, 404, "服务器不存在")
		return
	}
	res := gin.H{"server": a.view(&sv)}
	var ri store.RealityInbound
	if a.P.St.DB.First(&ri, "server_id = ?", id).Error == nil {
		res["reality"] = ri
	}
	var li store.LandingInbound
	if a.P.St.DB.First(&li, "server_id = ?", id).Error == nil {
		res["landing"] = li
		var users []store.LandingUser
		a.P.St.DB.Where("landing_server_id = ?", id).Find(&users)
		type lu struct {
			ID        uint   `json:"id"`
			Email     string `json:"email"`
			EntryID   uint   `json:"entry_server_id"`
			EntryName string `json:"entry_name"`
			Up        uint64 `json:"month_up"`
			Down      uint64 `json:"month_down"`
		}
		var list []lu
		for _, u := range users {
			up, down := a.P.Sum("landing_user", strconv.FormatUint(uint64(id), 10)+":"+u.Email, monthStart())
			list = append(list, lu{u.ID, u.Email, u.EntryServerID, a.P.ServerName(u.EntryServerID), up, down})
		}
		res["landing_users"] = list
	}
	var latest store.ConfigVersion
	if a.P.St.DB.Where("server_id = ?", id).Order("version DESC").Select("id, version, reason, applied, created_at").First(&latest).Error == nil {
		res["latest_config"] = latest
	}
	ok(c, res)
}

func (a *API) putReality(c *gin.Context) {
	var in service.RealityInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if err := a.P.UpdateReality(idParam(c), in); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "update_reality", a.P.ServerName(idParam(c)), in.Target, c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) putLanding(c *gin.Context) {
	var in service.LandingInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if err := a.P.UpdateLanding(idParam(c), in); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "update_landing", a.P.ServerName(idParam(c)), in.Method, c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) upgradeNode(c *gin.Context) {
	var in struct {
		CapReality bool `json:"cap_reality"`
		CapLanding bool `json:"cap_landing"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if err := a.P.UpgradeToNode(idParam(c), in.CapReality, in.CapLanding); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "upgrade_node", a.P.ServerName(idParam(c)), "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) downgradeNode(c *gin.Context) {
	if err := a.P.DowngradeToProbe(idParam(c)); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "downgrade_probe", a.P.ServerName(idParam(c)), "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) deployNode(c *gin.Context) {
	tid, err := a.P.Deploy(idParam(c), "手动下发", nil)
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"task_id": strconv.FormatUint(tid, 10)})
}

func (a *API) listConfigs(c *gin.Context) {
	var vs []store.ConfigVersion
	a.P.St.DB.Select("id, server_id, version, reason, applied, created_at").Where("server_id = ?", idParam(c)).Order("version DESC").Limit(100).Find(&vs)
	ok(c, vs)
}

func (a *API) getConfig(c *gin.Context) {
	var v store.ConfigVersion
	if err := a.P.St.DB.First(&v, "id = ? AND server_id = ?", c.Param("vid"), idParam(c)).Error; err != nil {
		fail(c, 404, "版本不存在")
		return
	}
	ok(c, gin.H{"version": v, "config": redactConfig(v.Config)})
}

func (a *API) rollbackConfig(c *gin.Context) {
	vid, _ := strconv.ParseUint(c.Param("vid"), 10, 64)
	if !a.P.Auth.VerifySensitive(c.GetHeader("X-Confirm")) {
		fail(c, http.StatusForbidden, "需要二次验证（2FA 验证码，未开启 2FA 时为登录密码）")
		return
	}
	tid, err := a.P.Rollback(idParam(c), uint(vid))
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "rollback_config", a.P.ServerName(idParam(c)), c.Param("vid"), c.ClientIP())
	ok(c, gin.H{"task_id": strconv.FormatUint(tid, 10)})
}

func (a *API) previewConfig(c *gin.Context) {
	raw, fw, err := a.P.BuildConfig(idParam(c))
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"config": redactConfig(string(prettyJSON(raw))), "firewall": fw})
}

// ---------- routes ----------

func (a *API) listRoutes(c *gin.Context) { ok(c, a.P.RouteViews()) }

func (a *API) createRoute(c *gin.Context) {
	var in service.RouteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	r, err := a.P.CreateRoute(in)
	if err != nil {
		if r != nil { // created but push failed: still report it
			ok(c, gin.H{"route": r, "warning": err.Error()})
			return
		}
		bad(c, err)
		return
	}
	a.P.Audit("route", "create", r.Name, r.Type, c.ClientIP())
	ok(c, gin.H{"route": r})
}

func (a *API) updateRoute(c *gin.Context) {
	var in service.RouteInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	r, err := a.P.UpdateRoute(idParam(c), in)
	if err != nil && r == nil {
		bad(c, err)
		return
	}
	a.P.Audit("route", "update", r.Name, "", c.ClientIP())
	res := gin.H{"route": r}
	if err != nil {
		res["warning"] = err.Error()
	}
	ok(c, res)
}

func (a *API) deleteRoute(c *gin.Context) {
	if err := a.P.DeleteRoute(idParam(c)); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("route", "delete", c.Param("id"), "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) toggleRoute(c *gin.Context) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	_ = c.ShouldBindJSON(&in)
	if err := a.P.SetRouteEnabled(idParam(c), in.Enabled); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"ok": true})
}

func (a *API) regenRoute(c *gin.Context) {
	if err := a.P.RegenRouteUUID(idParam(c)); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("route", "regen_uuid", c.Param("id"), "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) topology(c *gin.Context) { ok(c, a.P.Topology()) }

// ---------- subscriptions ----------

func (a *API) listSubTokens(c *gin.Context) {
	ok(c, gin.H{"tokens": a.P.SubTokens(), "base": a.P.SubBase()})
}

func (a *API) createSubToken(c *gin.Context) {
	var in struct{ Name, Target string }
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	t, err := a.P.CreateSubToken(in.Name, in.Target)
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("sub", "create_token", t.Name, "", c.ClientIP())
	ok(c, t)
}

func (a *API) updateSubToken(c *gin.Context) {
	var in struct {
		Name    string `json:"name"`
		Target  string `json:"target_default"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	a.P.St.DB.Model(&store.SubToken{}).Where("id = ?", idParam(c)).Updates(map[string]any{"name": in.Name, "target_default": in.Target, "enabled": in.Enabled})
	ok(c, gin.H{"ok": true})
}

func (a *API) resetSubToken(c *gin.Context) {
	if err := a.P.ResetSubToken(idParam(c)); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("sub", "reset_token", c.Param("id"), "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) deleteSubToken(c *gin.Context) {
	a.P.St.DB.Delete(&store.SubToken{}, idParam(c))
	a.P.Audit("sub", "delete_token", c.Param("id"), "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) previewSub(c *gin.Context) {
	s, err := a.P.PreviewSub(c.DefaultQuery("target", "mihomo"))
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"content": s, "nodes": len(a.P.SubNodes())})
}

func (a *API) subLogs(c *gin.Context) {
	tid, _ := strconv.ParseUint(c.Query("token_id"), 10, 64)
	ok(c, a.P.SubAccessLogs(uint(tid), 200))
}

func (a *API) listTemplates(c *gin.Context) {
	var ts []store.SubTemplate
	a.P.St.DB.Order("target, id").Find(&ts)
	ok(c, ts)
}

func (a *API) defaultTemplate(c *gin.Context) {
	ok(c, gin.H{"mihomo": subDefaultMihomo(), "singbox": subDefaultSingBox()})
}

func (a *API) saveTemplate(c *gin.Context) {
	var in store.SubTemplate
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if err := service.ValidateTemplate(in.Target, in.Content, a.P.SubNodes()); err != nil {
		bad(c, err)
		return
	}
	in.ID = idParam(c)
	db := a.P.St.DB
	if in.Active { // one active template per target
		db.Model(&store.SubTemplate{}).Where("target = ? AND id <> ?", in.Target, in.ID).Update("active", false)
	}
	if err := db.Save(&in).Error; err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("sub", "save_template", in.Name, in.Target, c.ClientIP())
	ok(c, in)
}

func (a *API) deleteTemplate(c *gin.Context) {
	a.P.St.DB.Delete(&store.SubTemplate{}, idParam(c))
	ok(c, gin.H{"ok": true})
}

func (a *API) rotateAll(c *gin.Context) {
	if !a.P.Auth.VerifySensitive(c.GetHeader("X-Confirm")) {
		fail(c, http.StatusForbidden, "需要二次验证（2FA 验证码，未开启 2FA 时为登录密码）")
		return
	}
	if err := a.P.RotateAll(); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("sub", "rotate_all", "", "重新生成所有 UUID / Reality 密钥 / SS 密钥", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

// serveSub is public: /s/{token}?target=mihomo|singbox|base64
func (a *API) serveSub(c *gin.Context) {
	res, err := a.P.ServeSub(c.Param("token"), c.Query("target"), c.GetHeader("User-Agent"), c.ClientIP())
	switch {
	case errors.Is(err, service.ErrSubNotFound):
		c.String(http.StatusNotFound, "not found")
		return
	case errors.Is(err, service.ErrSubRateLimited):
		c.String(http.StatusTooManyRequests, "too many requests")
		return
	case err != nil:
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	for k, v := range res.Headers {
		c.Header(k, v)
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, res.ContentType, res.Body)
}
