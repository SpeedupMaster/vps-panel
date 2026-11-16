package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/store"
)

func (a *API) login(c *gin.Context) {
	var in struct{ Username, Password, Code string }
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	ip := c.ClientIP()
	raw, err := a.P.Auth.Login(in.Username, in.Password, in.Code, ip, c.Request.UserAgent())
	switch {
	case errors.Is(err, auth.ErrNeedTOTP):
		ok(c, gin.H{"need_totp": true})
		return
	case err != nil:
		a.P.Audit("login", "login_failed", in.Username, err.Error(), ip)
		if a.P.Auth.Failures(ip) >= 3 {
			a.P.Alert.Info("login_failed", 0, "面板登录多次失败，来源 IP "+ip)
		}
		fail(c, http.StatusUnauthorized, err.Error())
		return
	}
	a.P.Audit("login", "login", in.Username, "登录成功", ip)
	a.notifyNewIP(ip)
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	http.SetCookie(c.Writer, &http.Cookie{Name: auth.CookieName, Value: raw, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: 7 * 86400})
	ok(c, gin.H{"ok": true})
}

// notifyNewIP alerts when the admin logs in from an IP not seen before.
func (a *API) notifyNewIP(ip string) {
	var n int64
	a.P.St.DB.Model(&store.AuditLog{}).Where("type = ? AND action = ? AND ip = ?", "login", "login", ip).Count(&n)
	if n <= 1 {
		var total int64
		a.P.St.DB.Model(&store.AuditLog{}).Where("type = ? AND action = ?", "login", "login").Count(&total)
		if total > 1 {
			a.P.Alert.Info("login_new_ip", 0, "面板从新 IP 登录："+ip)
		}
	}
}

func (a *API) logout(c *gin.Context) {
	raw, _ := c.Cookie(auth.CookieName)
	a.P.Auth.Logout(raw)
	http.SetCookie(c.Writer, &http.Cookie{Name: auth.CookieName, Value: "", Path: "/", MaxAge: -1})
	ok(c, gin.H{"ok": true})
}

func (a *API) me(c *gin.Context) {
	ad, _ := a.P.Auth.Admin()
	s := c.MustGet("session").(*store.Session)
	ok(c, gin.H{
		"username": ad.Username, "totp": ad.TOTPSecret != "", "password_at": ad.PasswordAt,
		"recovery_left": a.P.Auth.RecoveryLeft(), "session": secretPrefix(s.ID),
	})
}

func secretPrefix(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func (a *API) changePassword(c *gin.Context) {
	var in struct{ Old, New string }
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if err := a.P.Auth.ChangePassword(in.Old, in.New); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("login", "change_password", "", "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) totpBegin(c *gin.Context) {
	sec, uri, err := a.P.Auth.BeginTOTP()
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"secret": sec, "uri": uri})
}

func (a *API) totpConfirm(c *gin.Context) {
	var in struct{ Code string }
	_ = c.ShouldBindJSON(&in)
	codes, err := a.P.Auth.ConfirmTOTP(in.Code)
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("login", "enable_totp", "", "", c.ClientIP())
	ok(c, gin.H{"recovery_codes": codes})
}

func (a *API) totpDisable(c *gin.Context) {
	var in struct{ Code string }
	_ = c.ShouldBindJSON(&in)
	if err := a.P.Auth.DisableTOTP(in.Code); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("login", "disable_totp", "", "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}

func (a *API) totpRecovery(c *gin.Context) {
	codes, err := a.P.Auth.RegenerateRecovery()
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"recovery_codes": codes})
}

func (a *API) sessions(c *gin.Context) {
	cur := c.MustGet("session").(*store.Session)
	var out []gin.H
	for _, s := range a.P.Auth.Sessions() {
		out = append(out, gin.H{"id": s.ID, "ip": s.IP, "ua": s.UA, "last_seen": s.LastSeen, "created_at": s.CreatedAt, "current": s.ID == cur.ID})
	}
	ok(c, out)
}

func (a *API) kickSession(c *gin.Context) {
	if err := a.P.Auth.Kick(c.Param("sid")); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("login", "kick_session", "", "", c.ClientIP())
	ok(c, gin.H{"ok": true})
}
