package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/xrayconf"
	"vpspanel/internal/panel/store"
)

// ---------- deploy / versions ----------

func (p *Panel) latestVersion(serverID uint) *store.ConfigVersion {
	var v store.ConfigVersion
	if err := p.St.DB.Where("server_id = ?", serverID).Order("version DESC").First(&v).Error; err != nil {
		return nil
	}
	v.Config = p.OpenConfigText(v.Config)
	v.Diff = p.OpenConfigText(v.Diff)
	return &v
}

// Deploy renders the config, records a version when it changed, and pushes it.
func (p *Panel) Deploy(serverID uint, reason string, op *proto.XrayOp) (uint64, error) {
	lock := p.serverDeployLock(serverID)
	lock.Lock()
	defer lock.Unlock()

	raw, fw, err := p.BuildConfig(serverID)
	if err != nil {
		return 0, err
	}
	pretty := string(xrayconf.Pretty(raw))
	last := p.latestVersion(serverID)
	ver := 1
	if last != nil {
		ver = last.Version + 1
	}
	if last == nil || last.Config != pretty {
		prev := ""
		if last != nil {
			prev = last.Config
		}
		cv := &store.ConfigVersion{
			ServerID:  serverID,
			Version:   ver,
			Config:    p.sealConfigText(pretty),
			Diff:      p.sealConfigText(LineDiff(prev, pretty)),
			Reason:    reason,
			CreatedAt: time.Now(),
		}
		if err := p.St.DB.Create(cv).Error; err != nil {
			return 0, err
		}
		p.pruneVersions(serverID)
	} else {
		ver = last.Version
		if last.Applied && op == nil {
			p.syncFirewall(serverID, fw)
			return 0, nil
		}
	}
	if !p.Hub.Online(serverID) {
		return 0, nil
	}
	var sv store.Server
	p.St.DB.Select("xray_version").First(&sv, serverID)
	if sv.XrayVersion == "" {
		return 0, nil
	}
	if op != nil {
		op.FullConfig, op.Version = raw, ver
		t, err := p.newInternalTask(serverID, "xray_op", ver)
		if err != nil {
			return 0, err
		}
		op.TaskID = t.ID
		if err := p.Hub.Send(serverID, &proto.PanelMessage{XrayOp: op}); err != nil {
			return 0, err
		}
		if fw != nil {
			_ = p.Hub.Send(serverID, &proto.PanelMessage{ApplyConfig: &proto.ApplyConfig{Firewall: fw}})
		}
		return t.ID, nil
	}
	return p.pushApply(serverID, ver, raw, fw, false)
}

func compact(s string) []byte {
	var b bytes.Buffer
	if json.Compact(&b, []byte(s)) != nil {
		return []byte(s)
	}
	return b.Bytes()
}

func (p *Panel) pushApply(serverID uint, ver int, raw []byte, fw *proto.FirewallPolicy, restart bool) (uint64, error) {
	t, err := p.newInternalTask(serverID, "apply_config", ver)
	if err != nil {
		return 0, err
	}
	err = p.Hub.Send(serverID, &proto.PanelMessage{ApplyConfig: &proto.ApplyConfig{TaskID: t.ID, Version: ver, Config: raw, Firewall: fw, Restart: restart}})
	if err != nil {
		p.finishTask(t, "failed", "发送失败："+err.Error())
		return t.ID, err
	}
	if fw != nil {
		fb, _ := json.Marshal(fw)
		_ = p.St.Set(fmt.Sprintf("fw_hash_%d", serverID), xrayconf.Hash(fb))
	}
	return t.ID, nil
}

func (p *Panel) newInternalTask(serverID uint, typ string, ver int) (*store.Task, error) {
	exp := time.Now().Add(10 * time.Minute)
	t := &store.Task{
		ID: nextTaskID(), ServerID: serverID, Type: typ, Args: fmt.Sprintf(`{"version":"%d"}`, ver),
		Status: "sent", Timeout: 120, ExpireAt: &exp, CreatedAt: time.Now(),
	}
	return t, p.St.DB.Create(t).Error
}

func (p *Panel) pruneVersions(serverID uint) {
	keep := p.St.GetInt("config_versions_keep", 50)
	p.St.DB.Exec(`DELETE FROM config_versions WHERE server_id = ? AND id NOT IN
		(SELECT id FROM config_versions WHERE server_id = ? ORDER BY version DESC LIMIT ?)`, serverID, serverID, keep)
}

// Rollback re-applies an older version as a new version.
func (p *Panel) Rollback(serverID uint, versionID uint) (uint64, error) {
	lock := p.serverDeployLock(serverID)
	lock.Lock()
	defer lock.Unlock()

	var old store.ConfigVersion
	if err := p.St.DB.First(&old, "id = ? AND server_id = ?", versionID, serverID).Error; err != nil {
		return 0, errors.New("版本不存在")
	}
	oldCfg := p.OpenConfigText(old.Config)
	last := p.latestVersion(serverID)
	nextVer := 1
	prevCfg := ""
	if last != nil {
		nextVer = last.Version + 1
		prevCfg = last.Config
	}
	cv := &store.ConfigVersion{
		ServerID:  serverID,
		Version:   nextVer,
		Config:    p.sealConfigText(oldCfg),
		Diff:      p.sealConfigText(LineDiff(prevCfg, oldCfg)),
		Reason:    fmt.Sprintf("回滚到 v%d", old.Version),
		CreatedAt: time.Now(),
	}
	if err := p.St.DB.Create(cv).Error; err != nil {
		return 0, err
	}
	if !p.Hub.Online(serverID) {
		return 0, errors.New("Agent 不在线，上线后会自动同步")
	}
	_, fw, _ := p.BuildConfig(serverID)
	return p.pushApply(serverID, cv.Version, compact(oldCfg), fw, true)
}

func (p *Panel) redeployLandingAndEntries(landingID uint, reason string) error {
	if _, err := p.Deploy(landingID, reason, nil); err != nil {
		return err
	}
	for _, e := range p.entriesOfLanding(landingID) {
		if _, err := p.Deploy(e, reason, nil); err != nil {
			return err
		}
	}
	return nil
}

// onXrayState stores state and keeps the node in sync with the panel.
func (p *Panel) onXrayState(serverID uint, s *proto.XrayState) {
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return
	}
	p.St.DB.Model(&sv).Updates(map[string]any{
		"xray_version": s.Version, "xray_running": s.Running, "xray_config_ver": s.ConfigVer, "xray_drift": s.Drift,
	})
	sv.XrayVersion = s.Version
	if sv.Mode != "node" {
		return
	}
	if !s.Installed {
		if p.St.GetBool("auto_install_xray", true) && !p.hasActiveTask(serverID, proto.TaskInstallXray) {
			_, _ = p.CreateTask(serverID, proto.TaskInstallXray, p.xraySourceArgs(sv.XrayLockedVersion), nil, 600, "")
		}
		return
	}
	if p.hasActiveTask(serverID, "apply_config") || p.hasActiveTask(serverID, "xray_op") {
		return
	}
	last := p.latestVersion(serverID)
	if last == nil || !s.Drift && s.ConfigHash == xrayconf.Hash([]byte(last.Config)) {
		if last == nil && (sv.CapReality || sv.CapLanding) {
			go p.Deploy(serverID, "首次部署", nil)
		} else if last != nil && !last.Applied {
			p.markApplied(serverID, last.Version)
		}
		if last != nil && sv.CapLanding {
			go func() {
				if _, fw, err := p.BuildConfig(serverID); err == nil {
					p.syncFirewall(serverID, fw)
				}
			}()
		}
		return
	}
	if s.Drift && !p.St.GetBool("drift_autofix", true) {
		return
	}
	if p.recentFailedApply(serverID, 10*time.Minute) {
		return
	}
	go func() {
		if _, err := p.Deploy(serverID, "同步配置", nil); err == nil {
			if l := p.latestVersion(serverID); l != nil && xrayconf.Hash([]byte(l.Config)) != s.ConfigHash && !p.hasActiveTask(serverID, "apply_config") {
				_, fw, _ := p.BuildConfig(serverID)
				_, _ = p.pushApply(serverID, l.Version, compact(l.Config), fw, false)
			}
		}
	}()
}

func (p *Panel) hasActiveTask(serverID uint, typ string) bool {
	var n int64
	p.St.DB.Model(&store.Task{}).Where("server_id = ? AND type = ? AND status IN ? AND created_at > ?",
		serverID, typ, []string{"pending", "sent", "running"}, time.Now().Add(-15*time.Minute)).Count(&n)
	return n > 0
}

func (p *Panel) recentFailedApply(serverID uint, within time.Duration) bool {
	var t store.Task
	err := p.St.DB.Where("server_id = ? AND type IN ? AND finished_at IS NOT NULL", serverID, []string{"apply_config", "xray_op"}).
		Order("id DESC").Select("status, finished_at").First(&t).Error
	return err == nil && t.Status != "success" && t.FinishedAt != nil && time.Since(*t.FinishedAt) < within
}

func (p *Panel) markApplied(serverID uint, ver int) {
	p.St.DB.Model(&store.ConfigVersion{}).Where("server_id = ? AND version = ?", serverID, ver).Update("applied", true)
	p.St.DB.Model(&store.Server{}).Where("id = ?", serverID).Updates(map[string]any{"xray_config_ver": ver, "xray_drift": false})
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// ---------- mode changes & secret rotation ----------

// UpgradeToNode turns a probe-only machine into a node and installs Xray.
func (p *Panel) UpgradeToNode(serverID uint, capReality, capLanding bool) error {
	if !capReality && !capLanding {
		return errors.New("至少选择一种能力")
	}
	var sv store.Server
	if err := p.St.DB.First(&sv, serverID).Error; err != nil {
		return err
	}
	if sv.Mode == "node" {
		if !capReality {
			var n int64
			p.St.DB.Model(&store.Route{}).Where("entry_server_id = ?", serverID).Count(&n)
			if n > 0 {
				return errors.New("还有以这台机器为入口的线路")
			}
		}
		if !capLanding && len(p.entriesOfLanding(serverID)) > 0 {
			return errors.New("还有线路使用这台机器做落地")
		}
		if err := p.St.DB.Model(&sv).Updates(map[string]any{"cap_reality": capReality, "cap_landing": capLanding}).Error; err != nil {
			return err
		}
		_, err := p.Deploy(serverID, "修改节点能力", nil)
		return err
	}
	if err := p.St.DB.Model(&sv).Updates(map[string]any{"mode": "node", "cap_reality": capReality, "cap_landing": capLanding}).Error; err != nil {
		return err
	}
	if capReality {
		if _, err := p.EnsureReality(serverID); err != nil {
			return err
		}
	}
	if capLanding {
		if _, err := p.EnsureLanding(serverID); err != nil {
			return err
		}
	}
	_, err := p.CreateTask(serverID, proto.TaskInstallXray, p.xraySourceArgs(""), nil, 600, "")
	return err
}

// DowngradeToProbe uninstalls Xray. Routes must be removed first.
func (p *Panel) DowngradeToProbe(serverID uint) error {
	if p.serverReferencedByRoutes(serverID) {
		return errors.New("还有线路使用这台机器，请先删除相关线路")
	}
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&store.Server{}).Where("id = ?", serverID).Updates(map[string]any{
			"mode": "probe", "cap_reality": false, "cap_landing": false, "xray_version": "", "xray_running": false,
		}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&store.RealityInbound{}, "server_id = ?", serverID).Error; err != nil {
			return err
		}
		if err := tx.Delete(&store.LandingInbound{}, "server_id = ?", serverID).Error; err != nil {
			return err
		}
		return tx.Delete(&store.LandingUser{}, "landing_server_id = ?", serverID).Error
	}); err != nil {
		return err
	}
	_, err := p.CreateTask(serverID, proto.TaskUninstallXray, map[string]string{"downgrade": "1"}, nil, 120, "")
	return err
}

// RotateAll regenerates every secret clients depend on and redeploys every node.
func (p *Panel) RotateAll() error {
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		var rs []store.Route
		if err := tx.Find(&rs).Error; err != nil {
			return err
		}
		for i := range rs {
			if err := tx.Model(&rs[i]).Update("uuid", NewUUID()).Error; err != nil {
				return err
			}
		}
		var ris []store.RealityInbound
		if err := tx.Find(&ris).Error; err != nil {
			return err
		}
		for i := range ris {
			priv, pub := NewX25519()
			enc := p.Box.Seal(priv)
			if err := tx.Model(&ris[i]).Updates(map[string]any{"private_key_enc": enc, "public_key": pub, "short_ids": NewShortID()}).Error; err != nil {
				return err
			}
		}
		var lis []store.LandingInbound
		if err := tx.Find(&lis).Error; err != nil {
			return err
		}
		for i := range lis {
			enc := p.Box.Seal(SSKey(lis[i].Method))
			if err := tx.Model(&lis[i]).Update("server_key_enc", enc).Error; err != nil {
				return err
			}
			var us []store.LandingUser
			if err := tx.Where("landing_server_id = ?", lis[i].ServerID).Find(&us).Error; err != nil {
				return err
			}
			for j := range us {
				e := p.Box.Seal(SSKey(lis[i].Method))
				if err := tx.Model(&us[j]).Update("user_key_enc", e).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return err
	}
	var nodes []store.Server
	p.St.DB.Where("mode = ?", "node").Find(&nodes)
	for _, n := range nodes {
		if n.CapLanding {
			_, _ = p.Deploy(n.ID, "一键轮换", nil)
		}
	}
	for _, n := range nodes {
		if !n.CapLanding {
			_, _ = p.Deploy(n.ID, "一键轮换", nil)
		}
	}
	return nil
}
