package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/xrayconf"
	"vpspanel/internal/panel/store"
)

// ---------- batch updates ----------

type UpdateRequest struct {
	Component string `json:"component"` // agent | xray | geo
	Version   string `json:"version"`
	ServerIDs []uint `json:"server_ids"`
	Canary    bool   `json:"canary"` // update the first server, wait until healthy, then the rest
}

func (p *Panel) updateTask(sv *store.Server, req UpdateRequest) (string, map[string]string, int, error) {
	switch req.Component {
	case "agent":
		var r store.Release
		if err := p.St.DB.Where("component = ? AND version = ?", "agent", strings.TrimPrefix(req.Version, "v")).First(&r).Error; err != nil {
			return "", nil, 0, errors.New("Agent 版本不存在")
		}
		arch := "amd64"
		if c := p.Hub.Conn(sv.ID); c != nil && c.Hello() != nil && c.Hello().System.Arch != "" {
			arch = c.Hello().System.Arch
		}
		assets := map[string]AgentAsset{}
		_ = json.Unmarshal([]byte(r.Assets), &assets)
		a, ok := assets[arch]
		if !ok {
			return "", nil, 0, fmt.Errorf("该版本没有 %s 架构的文件", arch)
		}
		base := strings.TrimRight(p.St.Get("panel_url", ""), "/")
		if base == "" {
			return "", nil, 0, errors.New("请先在设置中填写面板地址")
		}
		return proto.TaskUpdateAgent, map[string]string{
			"url": fmt.Sprintf("%s/dl/agentv/%s/vpsagent-linux-%s", base, r.Version, arch), "sha256": a.SHA256, "sig": a.Sig, "version": r.Version,
		}, 300, nil
	case "xray":
		if sv.Mode != "node" {
			return "", nil, 0, errors.New("仅节点机可更新 Xray")
		}
		args := p.xraySourceArgs(strings.TrimPrefix(req.Version, "v"))
		if args["version"] == "" {
			return "", nil, 0, errors.New("没有可用的 Xray 版本")
		}
		return proto.TaskUpdateXray, args, 600, nil
	case "geo":
		if sv.Mode != "node" {
			return "", nil, 0, errors.New("仅节点机有规则文件")
		}
		return proto.TaskUpdateGeo, map[string]string{"source": p.St.Get("geo_source", "loyalsoldier")}, 300, nil
	}
	return "", nil, 0, errors.New("未知更新类型")
}

// StartUpdate validates the request and runs it in the background; progress
// is visible as tasks sharing the returned batch id.
func (p *Panel) StartUpdate(req UpdateRequest) (string, error) {
	if len(req.ServerIDs) == 0 {
		return "", errors.New("未选择服务器")
	}
	var svs []store.Server
	p.St.DB.Where("id IN ?", req.ServerIDs).Order("id").Find(&svs)
	if req.Component == "xray" { // servers pinned to a version keep it
		kept := svs[:0]
		for _, sv := range svs {
			if sv.XrayLockedVersion == "" || strings.TrimPrefix(sv.XrayLockedVersion, "v") == strings.TrimPrefix(req.Version, "v") {
				kept = append(kept, sv)
			}
		}
		svs = kept
		if len(svs) == 0 {
			return "", errors.New("所选服务器都锁定了 Xray 版本")
		}
	}
	for i := range svs {
		if _, _, _, err := p.updateTask(&svs[i], req); err != nil {
			return "", fmt.Errorf("%s：%w", svs[i].Name, err)
		}
	}
	batch := fmt.Sprintf("upd-%s-%d", req.Component, time.Now().Unix())
	go p.runUpdate(batch, req, svs)
	return batch, nil
}

func (p *Panel) runUpdate(batch string, req UpdateRequest, svs []store.Server) {
	label := map[string]string{"agent": "Agent", "xray": "Xray", "geo": "规则文件"}[req.Component]
	var okNames, failNames []string
	runOne := func(sv *store.Server) bool {
		typ, args, timeout, _ := p.updateTask(sv, req)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout+60)*time.Second)
		defer cancel()
		t, err := p.CreateTask(sv.ID, typ, args, nil, timeout, batch)
		if err != nil {
			return false
		}
		r := p.waitTask(ctx, t.ID)
		if r == nil || r.Status != "success" {
			return false
		}
		if req.Component == "agent" { // must come back with the new version
			return p.waitAgentVersion(sv.ID, args["version"], 120*time.Second)
		}
		return true
	}
	rest := svs
	if req.Canary && len(svs) > 1 {
		if !runOne(&svs[0]) {
			p.Alert.Info("update_result", svs[0].ID, fmt.Sprintf("%s 更新：试点机 %s 失败，已停止批量更新", label, svs[0].Name))
			return
		}
		okNames = append(okNames, svs[0].Name)
		rest = svs[1:]
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i := range rest {
		wg.Add(1)
		sem <- struct{}{}
		go func(sv *store.Server) {
			defer wg.Done()
			defer func() { <-sem }()
			okk := runOne(sv)
			mu.Lock()
			if okk {
				okNames = append(okNames, sv.Name)
			} else {
				failNames = append(failNames, sv.Name)
			}
			mu.Unlock()
		}(&rest[i])
	}
	wg.Wait()
	msg := fmt.Sprintf("%s 更新完成：成功 %d 台", label, len(okNames))
	if len(failNames) > 0 {
		msg += fmt.Sprintf("，失败 %d 台（%s）", len(failNames), strings.Join(failNames, "、"))
	}
	p.Alert.Info("update_result", 0, msg)
}

// waitTask blocks until a task finishes (also when it was already done).
func (p *Panel) waitTask(ctx context.Context, id uint64) *proto.TaskResult {
	ch := p.WatchTask(id)
	defer p.UnwatchTask(id, ch)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case r, ok := <-ch:
			if !ok || r.Finished {
				var t store.Task
				p.St.DB.First(&t, id)
				return &proto.TaskResult{TaskID: id, Status: t.Status, Finished: true}
			}
		case <-tick.C:
			var t store.Task
			if p.St.DB.First(&t, id).Error == nil && t.FinishedAt != nil {
				return &proto.TaskResult{TaskID: id, Status: t.Status, Finished: true}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (p *Panel) waitAgentVersion(id uint, ver string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		var sv store.Server
		if p.St.DB.Select("agent_version").First(&sv, id).Error == nil &&
			strings.TrimPrefix(sv.AgentVersion, "v") == strings.TrimPrefix(ver, "v") && p.Hub.Online(id) {
			return true
		}
	}
	return false
}

// CheckAgentVersions announces outdated agents once per published version.
func (p *Panel) CheckAgentVersions() {
	pub := p.AgentCurrent()
	if pub == "" || p.St.Get("agent_outdated_notified", "") == pub {
		return
	}
	var svs []store.Server
	p.St.DB.Where("agent_version <> ''").Find(&svs)
	var old []string
	for _, sv := range svs {
		if xrayconf.CompareVersion(strings.TrimPrefix(sv.AgentVersion, "v"), pub) < 0 {
			old = append(old, sv.Name)
		}
	}
	if len(old) > 0 {
		p.Alert.Info("agent_outdated", 0, fmt.Sprintf("%d 台 Agent 低于 %s：%s", len(old), pub, strings.Join(old, "、")))
	}
	_ = p.St.Set("agent_outdated_notified", pub)
}
