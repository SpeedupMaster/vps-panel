// Package api is the panel's REST API (JSON, cookie session).
package api

import (
	"bytes"
	"compress/gzip"
	"html"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/service"
)

type API struct {
	P              *service.Panel
	Web            fs.FS // built SPA (may be empty during development)
	Script         []byte
	TrustedProxies []string
	Extra          []func(r *gin.Engine, authed *gin.RouterGroup) // feature modules
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
	r.Use(a.subHostOnly)
	proxies := a.TrustedProxies
	if len(proxies) == 0 {
		proxies = []string{"127.0.0.1", "::1"}
	}
	_ = r.SetTrustedProxies(proxies)

	// Public endpoints (no session): install script, agent download, subscription.
	r.GET("/install.sh", a.installScript)
	r.GET("/dl/agent/:file", a.downloadAgent)
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "ok") })
	r.GET("/status", a.statusPage) // public status page, off unless status_enabled

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
	authed.GET("/servers/:id/traffic", a.serverTraffic)

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

func hostOnly(hostport string) string {
	h := strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(h); err == nil {
		return strings.ToLower(host)
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

// subHostOnly restricts a dedicated subscription domain (sub_url with a host
// distinct from panel_url) to subscription endpoints only, returning 404 for
// the panel UI, API, and all other paths.
func (a *API) subHostOnly(c *gin.Context) {
	subHost := a.P.SubOnlyHost()
	if subHost == "" || hostOnly(c.Request.Host) != subHost {
		return
	}
	if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		p := c.Request.URL.Path
		var tok string
		if strings.HasPrefix(p, "/s/") {
			tok = strings.TrimPrefix(p, "/s/")
		} else {
			tok = strings.TrimPrefix(p, "/")
		}
		if tok != "" && !strings.Contains(tok, "/") {
			a.serveSubToken(c, tok)
			c.Abort()
			return
		}
	}
	c.String(http.StatusNotFound, "not found")
	c.Abort()
}

// tryRootSub serves root-level subscription URLs (/<token>) when the path is a
// single segment matching a stored subscription token.
func (a *API) tryRootSub(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	seg := strings.TrimPrefix(c.Request.URL.Path, "/")
	if seg == "" || strings.Contains(seg, "/") || !a.P.HasSubToken(seg) {
		return false
	}
	a.serveSubToken(c, seg)
	return true
}

// mountSPA serves root-level subscription tokens and the embedded UI. With
// admin_path set, the UI is served only under that prefix.
func (a *API) mountSPA(r *gin.Engine) {
	var fileServer http.Handler
	if a.Web != nil {
		fileServer = http.FileServer(http.FS(a.Web))
	}
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/s/") {
			fail(c, http.StatusNotFound, "not found")
			return
		}
		prefix := "/" + strings.Trim(a.P.St.Get("admin_path", ""), "/")
		if prefix != "/" {
			if path == prefix && a.Web != nil {
				c.Redirect(http.StatusFound, prefix+"/")
				return
			}
			if !strings.HasPrefix(path, prefix+"/") {
				if a.tryRootSub(c) {
					return
				}
				c.Status(http.StatusNotFound)
				return
			}
			path = strings.TrimPrefix(path, prefix)
		} else if a.tryRootSub(c) {
			return
		}
		if a.Web == nil {
			c.Status(http.StatusNotFound)
			return
		}
		name := strings.TrimPrefix(path, "/")
		if name == "" {
			name = "index.html"
		}
		gzipped := false
		if st, err := fs.Stat(a.Web, name); err != nil || st.IsDir() {
			if st, err := fs.Stat(a.Web, name+".gz"); err == nil && !st.IsDir() {
				gzipped = true
			} else {
				name = "index.html" // SPA fallback (and no directory listings)
			}
		}
		if name == "index.html" {
			// Written directly: http.FileServer redirects /index.html to ./,
			// which would loop with the rewrite below.
			b, err := fs.ReadFile(a.Web, "index.html")
			if err != nil {
				c.Status(http.StatusNotFound)
				return
			}
			c.Header("Cache-Control", "no-cache")
			c.Data(http.StatusOK, "text/html; charset=utf-8", withBase(b, prefix))
			return
		}
		if strings.HasSuffix(name, ".webmanifest") {
			c.Header("Content-Type", "application/manifest+json")
		}
		if name == "sw.js" || strings.HasSuffix(name, ".webmanifest") {
			c.Header("Cache-Control", "no-cache")
		} else if strings.HasPrefix(name, "assets/") {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		if gzipped {
			serveGzipFS(c, a.Web, name)
			return
		}
		c.Request.URL.Path = "/" + name
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

func acceptsGzip(hdr string) bool {
	for _, part := range strings.Split(hdr, ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			if strings.Contains(strings.ReplaceAll(strings.ToLower(params), " ", ""), "q=0") &&
				!strings.Contains(strings.ReplaceAll(strings.ToLower(params), " ", ""), "q=0.") {
				return false
			}
			return true
		}
	}
	return false
}

func serveGzipFS(c *gin.Context, fsys fs.FS, name string) {
	f, err := fsys.Open(name + ".gz")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	if ct := c.Writer.Header().Get("Content-Type"); ct == "" {
		if m := mime.TypeByExtension(filepath.Ext(name)); m != "" {
			c.Header("Content-Type", m)
		}
	}
	c.Header("Vary", "Accept-Encoding")
	if acceptsGzip(c.GetHeader("Accept-Encoding")) {
		c.Header("Content-Encoding", "gzip")
		if rs, ok := f.(io.ReadSeeker); ok {
			http.ServeContent(c.Writer, c.Request, name, time.Time{}, rs)
			return
		}
		c.Status(http.StatusOK)
		if c.Request.Method != http.MethodHead {
			_, _ = io.Copy(c.Writer, f)
		}
		return
	}
	gr, err := gzip.NewReader(f)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	defer gr.Close()
	c.Status(http.StatusOK)
	if c.Request.Method != http.MethodHead {
		_, _ = io.Copy(c.Writer, gr)
	}
}

// withBase adds <base href> to index.html so the history-mode router and the
// relative asset URLs resolve against the panel root (prefix is "/" or the
// admin_path) even on deep links like /servers/3.
func withBase(page []byte, prefix string) []byte {
	base := strings.TrimSuffix(prefix, "/") + "/"
	tag := []byte(`<base href="` + html.EscapeString(base) + `">`)
	i := bytes.Index(page, []byte("<head>"))
	if i < 0 {
		return page
	}
	i += len("<head>")
	out := make([]byte, 0, len(page)+len(tag))
	out = append(out, page[:i]...)
	out = append(out, tag...)
	return append(out, page[i:]...)
}
