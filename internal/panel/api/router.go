// Package api is the panel's REST API (JSON, cookie session).
package api

import (
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/service"
)

type API struct {
	P      *service.Panel
	Web    fs.FS // built SPA (may be empty during development)
	Script []byte
	Extra  []func(r *gin.Engine, authed *gin.RouterGroup) // feature modules
}

func ok(c *gin.Context, v any) { c.JSON(http.StatusOK, v) }

func fail(c *gin.Context, code int, msg string) {
	c.AbortWithStatusJSON(code, gin.H{"error": msg})
}

func bad(c *gin.Context, err error) { fail(c, http.StatusBadRequest, err.Error()) }

func idParam(c *gin.Context) uint {
	v, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	return uint(v)
}

// Router builds the gin engine.
func (a *API) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	_ = r.SetTrustedProxies([]string{"127.0.0.1", "::1"})

	// Public endpoints (no session): install script, agent download, subscription.
	r.GET("/install.sh", a.installScript)
	r.GET("/dl/agent/:file", a.downloadAgent)
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })

	api := r.Group("/api", a.ipAllow)
	api.POST("/auth/login", a.login)
	api.POST("/auth/logout", a.logout)

	authed := api.Group("", a.requireSession)
	authed.GET("/auth/me", a.me)
	authed.POST("/account/password", a.changePassword)
	authed.POST("/account/totp/begin", a.totpBegin)
	authed.POST("/account/totp/confirm", a.totpConfirm)
	authed.POST("/account/totp/disable", a.totpDisable)
	authed.POST("/account/totp/recovery", a.totpRecovery)
	authed.GET("/account/sessions", a.sessions)
	authed.DELETE("/account/sessions/:sid", a.kickSession)

	authed.GET("/overview", a.overview)
	authed.GET("/servers", a.listServers)
	authed.POST("/servers", a.createServer)
	authed.GET("/servers/:id", a.getServer)
	authed.PUT("/servers/:id", a.updateServer)
	authed.DELETE("/servers/:id", a.deleteServer)
	authed.POST("/servers/:id/token", a.newToken)
	authed.GET("/servers/:id/metrics", a.serverMetrics)

	authed.GET("/tasks", a.listTasks)
	authed.GET("/tasks/:id", a.getTask)
	authed.GET("/tasks/:id/stream", a.streamTask)
	authed.GET("/events", a.events)

	authed.GET("/audit", a.audit)
	authed.GET("/settings", a.getSettings)
	authed.PUT("/settings", a.putSettings)

	a.mountNodes(r, authed)
	a.mountAlerts(authed)
	a.mountOps(r, authed)
	for _, f := range a.Extra {
		f(r, authed)
	}

	a.mountSPA(r)
	return r
}

func (a *API) ipAllow(c *gin.Context) {
	if !auth.IPAllowed(a.P.St.Get("allow_ips", ""), c.ClientIP()) {
		fail(c, http.StatusForbidden, "当前 IP 不允许访问面板")
	}
}

func (a *API) requireSession(c *gin.Context) {
	raw, _ := c.Cookie(auth.CookieName)
	s, okk := a.P.Auth.Check(raw, c.ClientIP())
	if !okk {
		fail(c, http.StatusUnauthorized, "未登录或会话已过期")
		return
	}
	c.Set("session", s)
	// Same-origin check for state-changing requests (cookie is SameSite=Strict
	// too; this guards older browsers).
	if c.Request.Method != http.MethodGet && c.GetHeader("X-Requested-With") != "vps-panel" {
		fail(c, http.StatusForbidden, "missing X-Requested-With")
	}
}

// mountSPA serves the embedded UI. With admin_path set, the UI is served
// only under that prefix.
func (a *API) mountSPA(r *gin.Engine) {
	if a.Web == nil {
		return
	}
	fileServer := http.FileServer(http.FS(a.Web))
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/s/") {
			fail(c, http.StatusNotFound, "not found")
			return
		}
		prefix := "/" + strings.Trim(a.P.St.Get("admin_path", ""), "/")
		if prefix != "/" {
			if path == prefix {
				c.Redirect(http.StatusFound, prefix+"/")
				return
			}
			if !strings.HasPrefix(path, prefix+"/") {
				c.Status(http.StatusNotFound)
				return
			}
			path = strings.TrimPrefix(path, prefix)
		}
		name := strings.TrimPrefix(path, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(a.Web, name); err != nil {
			name = "index.html" // SPA fallback
		}
		if name == "index.html" || name == "sw.js" {
			c.Header("Cache-Control", "no-cache")
		} else if strings.HasPrefix(name, "assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		c.Request.URL.Path = "/" + name
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}
