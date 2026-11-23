package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/version"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/service"
	"vpspanel/internal/panel/store"
)

func (a *API) mountOps(r *gin.Engine, g *gin.RouterGroup) {
	// Public downloads for agents.
	r.GET("/dl/xray/:ver/:file", a.xrayRelay)
	r.GET("/dl/agentv/:ver/:file", a.agentRelease)

	g.GET("/releases/:component", a.listReleases)
	g.POST("/releases/xray/sync", a.syncXray)
	g.POST("/releases/agent", a.uploadAgent)
	g.POST("/releases/agent/:ver/publish", a.publishAgent)
	g.DELETE("/releases/agent/:ver", a.deleteAgent)
	g.POST("/updates", a.startUpdate)
	g.GET("/updates/history", a.updateHistory)
	g.GET("/system/version", a.panelVersion)

	g.GET("/tools", a.toolMeta)
	g.POST("/servers/:id/tools/:tool", a.runTool)
	g.POST("/tools/iperf", a.runIperf)
	g.GET("/servers/:id/tool-results", a.toolResults)
	g.GET("/tool-results", a.toolHistory)

	g.GET("/scripts", a.listScripts)
	g.POST("/scripts", a.saveScript)
	g.PUT("/scripts/:id", a.saveScript)
	g.DELETE("/scripts/:id", a.deleteScript)
	g.POST("/scripts/run", a.runScript)

	g.GET("/schedules", a.listSchedules)
	g.POST("/schedules", a.saveSchedule)
	g.PUT("/schedules/:id", a.saveSchedule)
	g.DELETE("/schedules/:id", a.deleteSchedule)
	g.POST("/schedules/:id/run", a.runSchedule)

	g.GET("/backups", a.listBackups)
	g.POST("/backups", a.createBackup)
	g.GET("/backups/:name", a.downloadBackup)
	g.DELETE("/backups/:name", a.deleteBackup)
	g.POST("/backups/restore", a.restoreBackup)

	g.POST("/servers/:id/ssh-install", a.sshInstall)
	g.POST("/servers/:id/shell-ticket", a.shellTicket)
	g.GET("/servers/:id/shell", a.shell)
}

// ---------- downloads ----------

func (a *API) xrayRelay(c *gin.Context) {
	if !a.P.St.GetBool("xray_relay", false) {
		c.Status(http.StatusNotFound)
		return
	}
	path, err := a.P.XrayRelayFile(c.Param("ver"), c.Param("file"))
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	c.File(path)
}

func (a *API) agentRelease(c *gin.Context) {
	path, okk := a.P.AgentReleaseFile(strings.TrimPrefix(c.Param("ver"), "v"), c.Param("file"))
	if !okk {
		c.Status(http.StatusNotFound)
		return
	}
	serveAgentFile(c, path)
}

// ---------- releases & updates ----------

func (a *API) listReleases(c *gin.Context) {
	comp := c.Param("component")
	type view struct {
		store.Release
		Published bool `json:"published,omitempty"`
	}
	var out []view
	pub := a.P.AgentCurrent()
	for _, r := range a.P.Releases(comp) {
		out = append(out, view{r, comp == "agent" && r.Version == pub})
	}
	// Version spread across servers for the update center.
	var svs []store.Server
	a.P.St.DB.Select("id, name, mode, agent_version, xray_version, xray_locked_version").Find(&svs)
	ok(c, gin.H{
		"items": out, "servers": svs, "latest_xray": a.P.LatestXray(false),
		"synced_at": a.P.St.Get("xray_releases_synced_at", ""), "agent_published": pub,
		"signing": a.P.St.Get("sign_pubkey", "") != "",
	})
}

func (a *API) syncXray(c *gin.Context) {
	if err := a.P.SyncXrayReleases(); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"latest": a.P.LatestXray(false)})
}

func (a *API) uploadAgent(c *gin.Context) {
	ver := c.PostForm("version")
	notes := c.PostForm("notes")
	form, err := c.MultipartForm()
	if err != nil {
		bad(c, err)
		return
	}
	var added []string
	for _, arch := range []string{"amd64", "arm64", "arm", "386"} {
		fhs := form.File["file_"+arch]
		if len(fhs) == 0 {
			continue
		}
		f, err := fhs[0].Open()
		if err != nil {
			bad(c, err)
			return
		}
		_, err = a.P.AddAgentReleaseFromReader(ver, arch, io.LimitReader(f, 100<<20), c.PostForm("sig_"+arch), notes)
		f.Close()
		if err != nil {
			bad(c, errors.New(arch+"："+err.Error()))
			return
		}
		added = append(added, arch)
	}
	if len(added) == 0 {
		bad(c, errors.New("请至少上传一个架构的文件（file_amd64 / file_arm64）"))
		return
	}
	if c.PostForm("publish") == "1" {
		if err := a.P.PublishAgent(ver); err != nil {
			bad(c, err)
			return
		}
	}
	a.P.Audit("system", "agent_release", ver, strings.Join(added, ","), c.ClientIP())
	ok(c, gin.H{"version": ver, "archs": added})
}

func (a *API) publishAgent(c *gin.Context) {
	if err := a.P.PublishAgent(c.Param("ver")); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("system", "agent_publish", c.Param("ver"), "", c.ClientIP())
	ok(c, gin.H{})
}

func (a *API) deleteAgent(c *gin.Context) {
	if err := a.P.DeleteAgentRelease(c.Param("ver")); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{})
}

func (a *API) startUpdate(c *gin.Context) {
	var in service.UpdateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	batch, err := a.P.StartUpdate(in)
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("system", "update_"+in.Component, in.Version, batch, c.ClientIP())
	ok(c, gin.H{"batch": batch})
}

func (a *API) updateHistory(c *gin.Context) {
	var ts []store.Task
	a.P.St.DB.Where("type IN ?", []string{proto.TaskUpdateAgent, proto.TaskUpdateXray, proto.TaskUpdateGeo}).
		Order("id desc").Limit(200).Find(&ts)
	type view struct {
		store.Task
		ServerName string `json:"server_name"`
	}
	out := make([]view, 0, len(ts))
	for _, t := range ts {
		t.Output = lastLines(t.Output, 3)
		out = append(out, view{t, a.P.ServerName(t.ServerID)})
	}
	ok(c, out)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (a *API) panelVersion(c *gin.Context) {
	ok(c, gin.H{"current": version.Version})
}

// ---------- tools ----------

func (a *API) toolMeta(c *gin.Context) {
	ok(c, gin.H{"tools": service.Tools, "schedule_actions": service.ScheduleActions})
}

func (a *API) runTool(c *gin.Context) {
	var args map[string]string
	_ = c.ShouldBindJSON(&args)
	tool := c.Param("tool")
	if tool == "restart_agent" || tool == "tune_net" || tool == "restore_net" {
		a.P.Audit("server", tool, a.P.ServerName(idParam(c)), "", c.ClientIP())
	}
	id, err := a.P.RunTool(idParam(c), tool, args)
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"task_id": strconv.FormatUint(id, 10)})
}

func (a *API) runIperf(c *gin.Context) {
	var in struct {
		From uint `json:"from"`
		To   uint `json:"to"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.From == 0 || in.To == 0 || in.From == in.To {
		bad(c, errors.New("请选择两台不同的服务器"))
		return
	}
	id, err := a.P.RunIperf(in.From, in.To)
	if err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{"task_id": strconv.FormatUint(id, 10)})
}

func (a *API) toolResults(c *gin.Context) {
	ok(c, a.P.LatestToolResults(idParam(c)))
}

func (a *API) toolHistory(c *gin.Context) {
	q := a.P.St.DB.Order("id desc").Limit(200)
	if t := c.Query("tool"); t != "" {
		q = q.Where("tool = ?", t)
	}
	if s := c.Query("server_id"); s != "" {
		q = q.Where("server_id = ?", s)
	}
	var rs []store.ToolResult
	q.Find(&rs)
	ok(c, rs)
}

// ---------- scripts & schedules ----------

func (a *API) listScripts(c *gin.Context) {
	var ss []store.Script
	a.P.St.DB.Order("id").Find(&ss)
	ok(c, ss)
}

func (a *API) saveScript(c *gin.Context) {
	var s store.Script
	if err := c.ShouldBindJSON(&s); err != nil {
		bad(c, err)
		return
	}
	s.ID = idParam(c)
	if err := a.P.SaveScript(&s); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("system", "save_script", s.Name, s.SHA256[:12], c.ClientIP())
	ok(c, s)
}

func (a *API) deleteScript(c *gin.Context) {
	a.P.St.DB.Delete(&store.Script{}, idParam(c))
	ok(c, gin.H{})
}

func (a *API) runScript(c *gin.Context) {
	var in struct {
		ScriptID  uint   `json:"script_id"`
		Content   string `json:"content"`
		ServerIDs []uint `json:"server_ids"`
		Timeout   int    `json:"timeout"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	if !a.P.Auth.VerifySensitive(c.GetHeader("X-Confirm")) {
		fail(c, http.StatusForbidden, "二次验证失败")
		return
	}
	batch, ids, err := a.P.RunScript(in.ScriptID, in.Content, in.ServerIDs, in.Timeout)
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "run_script", strconv.Itoa(int(in.ScriptID)), batch, c.ClientIP())
	sids := make([]string, len(ids))
	for i, id := range ids {
		sids[i] = strconv.FormatUint(id, 10)
	}
	ok(c, gin.H{"batch": batch, "task_ids": sids})
}

func (a *API) listSchedules(c *gin.Context) {
	var ss []store.Schedule
	a.P.St.DB.Order("id").Find(&ss)
	ok(c, ss)
}

func (a *API) saveSchedule(c *gin.Context) {
	var s store.Schedule
	if err := c.ShouldBindJSON(&s); err != nil {
		bad(c, err)
		return
	}
	s.ID = idParam(c)
	if err := service.ValidateSchedule(&s); err != nil {
		bad(c, err)
		return
	}
	if s.ID != 0 {
		var old store.Schedule
		if a.P.St.DB.First(&old, s.ID).Error == nil {
			s.LastRun = old.LastRun
		}
	}
	if err := a.P.St.DB.Save(&s).Error; err != nil {
		bad(c, err)
		return
	}
	ok(c, s)
}

func (a *API) deleteSchedule(c *gin.Context) {
	a.P.St.DB.Delete(&store.Schedule{}, idParam(c))
	ok(c, gin.H{})
}

func (a *API) runSchedule(c *gin.Context) {
	var s store.Schedule
	if err := a.P.St.DB.First(&s, idParam(c)).Error; err != nil {
		fail(c, 404, "不存在")
		return
	}
	a.P.RunScheduleNow(&s)
	a.P.St.DB.Model(&s).Update("last_run", time.Now())
	ok(c, gin.H{})
}

// ---------- backups ----------

func (a *API) listBackups(c *gin.Context) {
	ok(c, gin.H{
		"items": a.P.Backups(), "last": a.P.St.Get("backup_last", ""), "last_error": a.P.St.Get("backup_last_error", ""),
		"encrypted": a.P.SecretSetting("backup_password") != "",
	})
}

func (a *API) createBackup(c *gin.Context) {
	name, err := a.P.BackupNow("manual")
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("system", "backup", name, "", c.ClientIP())
	ok(c, gin.H{"name": name, "warning": a.P.St.Get("backup_last_error", "")})
}

func (a *API) downloadBackup(c *gin.Context) {
	path, okk := a.P.BackupPath(c.Param("name"))
	if !okk {
		fail(c, 404, "备份不存在")
		return
	}
	a.P.Audit("system", "backup_download", c.Param("name"), "", c.ClientIP())
	c.FileAttachment(path, c.Param("name"))
}

func (a *API) deleteBackup(c *gin.Context) {
	if err := a.P.DeleteBackup(c.Param("name")); err != nil {
		bad(c, err)
		return
	}
	ok(c, gin.H{})
}

// restoreBackup needs TOTP (or the password without 2FA); the panel restarts.
func (a *API) restoreBackup(c *gin.Context) {
	if !a.P.Auth.VerifySensitive(c.GetHeader("X-Confirm")) {
		fail(c, http.StatusForbidden, "二次验证失败")
		return
	}
	var srcPath string
	if name := c.PostForm("name"); name != "" {
		path, okk := a.P.BackupPath(name)
		if !okk {
			bad(c, errors.New("备份不存在"))
			return
		}
		srcPath = path
	} else {
		fh, err := c.FormFile("file")
		if err != nil {
			bad(c, errors.New("请选择备份文件"))
			return
		}
		f, err := fh.Open()
		if err != nil {
			bad(c, err)
			return
		}
		tmp, err := os.CreateTemp(a.P.DataDir, ".restore-upload-*")
		if err != nil {
			f.Close()
			bad(c, err)
			return
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		_, copyErr := io.Copy(tmp, io.LimitReader(f, 2<<30))
		closeErr := tmp.Close()
		f.Close()
		if copyErr != nil {
			bad(c, copyErr)
			return
		}
		if closeErr != nil {
			bad(c, closeErr)
			return
		}
		srcPath = tmpPath
	}
	if err := a.P.StageRestoreFile(srcPath, c.PostForm("password")); err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("system", "restore", c.PostForm("name"), "", c.ClientIP())
	_, _ = a.P.BackupNow("pre-restore")
	ok(c, gin.H{"restarting": true})
	go func() {
		time.Sleep(time.Second)
		os.Exit(0)
	}()
}

// ---------- WebSSH ----------

type shellTicket struct {
	server uint
	exp    time.Time
}

var (
	ticketsMu sync.Mutex
	tickets   = map[string]shellTicket{}
)

// shellTicket checks the second factor once and returns a single-use ticket
// for the websocket (browsers can't set headers on websocket requests).
func (a *API) shellTicket(c *gin.Context) {
	if !a.P.Auth.VerifySensitive(c.GetHeader("X-Confirm")) {
		fail(c, http.StatusForbidden, "二次验证失败")
		return
	}
	if !a.P.Hub.Online(idParam(c)) {
		bad(c, errors.New("Agent 不在线"))
		return
	}
	t := secret.RandomToken(24)
	ticketsMu.Lock()
	for k, v := range tickets {
		if time.Now().After(v.exp) {
			delete(tickets, k)
		}
	}
	tickets[t] = shellTicket{idParam(c), time.Now().Add(time.Minute)}
	ticketsMu.Unlock()
	a.P.Audit("server", "webssh", a.P.ServerName(idParam(c)), "", c.ClientIP())
	ok(c, gin.H{"ticket": t})
}

type shellMsg struct {
	T string `json:"t"` // i = input, r = resize
	D string `json:"d,omitempty"`
	C int    `json:"c,omitempty"`
	R int    `json:"r,omitempty"`
}

func (a *API) shell(c *gin.Context) {
	sid := idParam(c)
	ticketsMu.Lock()
	tk, found := tickets[c.Query("ticket")]
	delete(tickets, c.Query("ticket"))
	ticketsMu.Unlock()
	if !found || tk.server != sid || time.Now().After(tk.exp) {
		fail(c, http.StatusForbidden, "票据无效或已过期")
		return
	}
	srv := websocket.Server{
		Handshake: func(cfg *websocket.Config, r *http.Request) error {
			o, err := url.Parse(r.Header.Get("Origin"))
			if err != nil || o.Host != r.Host {
				return errors.New("cross-origin websocket")
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			defer ws.Close()
			t, err := a.P.CreateTask(sid, proto.TaskShell, nil, nil, 4*3600, "")
			if err != nil {
				_ = websocket.Message.Send(ws, "\r\n"+err.Error()+"\r\n")
				return
			}
			ch := a.P.WatchTaskN(t.ID, 4096)
			defer a.P.UnwatchTask(t.ID, ch)
			done := make(chan struct{})
			go func() { // agent -> browser
				defer close(done)
				for r := range ch {
					if r.Output != "" {
						if websocket.Message.Send(ws, r.Output) != nil {
							return
						}
					}
					if r.Finished {
						msg := "\r\n[会话已结束]\r\n"
						if r.Status == "failed" {
							var ft store.Task
							a.P.St.DB.First(&ft, t.ID)
							msg = "\r\n" + lastLines(ft.Output, 2) + "\r\n"
						}
						_ = websocket.Message.Send(ws, msg)
						return
					}
				}
			}()
			for { // browser -> agent
				var raw string
				if err := websocket.Message.Receive(ws, &raw); err != nil {
					break
				}
				var m shellMsg
				if json.Unmarshal([]byte(raw), &m) != nil {
					continue
				}
				in := &proto.ShellInput{TaskID: t.ID}
				switch m.T {
				case "i":
					in.Data = []byte(m.D)
				case "r":
					in.Cols, in.Rows = m.C, m.R
				default:
					continue
				}
				if a.P.Hub.Send(sid, &proto.PanelMessage{ShellInput: in}) != nil {
					break
				}
			}
			_ = a.P.Hub.Send(sid, &proto.PanelMessage{ShellInput: &proto.ShellInput{TaskID: t.ID, Close: true}})
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		},
	}
	srv.ServeHTTP(c.Writer, c.Request)
}

func (a *API) sshInstall(c *gin.Context) {
	var in service.SSHInstallInput
	if err := c.ShouldBindJSON(&in); err != nil {
		bad(c, err)
		return
	}
	id, err := a.P.SSHInstall(idParam(c), in)
	if err != nil {
		bad(c, err)
		return
	}
	a.P.Audit("server", "ssh_install", a.P.ServerName(idParam(c)), in.Host, c.ClientIP())
	ok(c, gin.H{"task_id": strconv.FormatUint(id, 10)})
}
