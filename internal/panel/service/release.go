package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm/clause"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/version"
	"vpspanel/internal/common/xrayconf"
	"vpspanel/internal/panel/store"
)

var ghClient = &http.Client{Timeout: 30 * time.Second}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	Body        string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func ghGet(ctx context.Context, url string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "vps-panel")
	resp, err := ghClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(v)
}

// SyncXrayReleases caches the latest Xray-core releases and announces new
// stable versions once.
func (p *Panel) SyncXrayReleases() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var rs []ghRelease
	if err := ghGet(ctx, "https://api.github.com/repos/XTLS/Xray-core/releases?per_page=20", &rs); err != nil {
		return err
	}
	for _, r := range rs {
		if r.Draft {
			continue
		}
		ver := strings.TrimPrefix(r.TagName, "v")
		ch := "stable"
		if r.Prerelease {
			ch = "pre"
		}
		notes := r.Body
		if len(notes) > 8000 {
			notes = notes[:8000] + "…"
		}
		var cur store.Release
		if p.St.DB.Where("component = ? AND version = ?", "xray", ver).First(&cur).Error == nil {
			p.St.DB.Model(&cur).Updates(map[string]any{"channel": ch, "notes": notes})
			continue
		}
		p.St.DB.Create(&store.Release{Component: "xray", Version: ver, Channel: ch, Notes: notes, PublishedAt: r.PublishedAt})
	}
	_ = p.St.Set("xray_releases_synced_at", time.Now().Format(time.RFC3339))
	if latest := p.LatestXray(false); latest != "" && p.St.Get("xray_notified_ver", "") != latest {
		var behind []string
		var svs []store.Server
		p.St.DB.Where("mode = ? AND xray_version <> ''", "node").Find(&svs)
		for _, sv := range svs {
			if xrayconf.CompareVersion(sv.XrayVersion, latest) < 0 {
				behind = append(behind, sv.Name)
			}
		}
		if len(behind) > 0 {
			p.Alert.Info("new_version", 0, fmt.Sprintf("Xray %s 已发布，%d 台节点可更新：%s", latest, len(behind), strings.Join(behind, "、")))
		}
		_ = p.St.Set("xray_notified_ver", latest)
	}
	return nil
}

// LatestXray returns the newest cached version (stable only unless pre).
func (p *Panel) LatestXray(pre bool) string {
	var rs []store.Release
	q := p.St.DB.Where("component = ?", "xray")
	if !pre {
		q = q.Where("channel = ?", "stable")
	}
	q.Find(&rs)
	best := ""
	for _, r := range rs {
		if best == "" || xrayconf.CompareVersion(r.Version, best) > 0 {
			best = r.Version
		}
	}
	return best
}

func (p *Panel) Releases(component string) []store.Release {
	var rs []store.Release
	p.St.DB.Where("component = ?", component).Find(&rs)
	sort.SliceStable(rs, func(i, j int) bool {
		if c := xrayconf.CompareVersion(rs[i].Version, rs[j].Version); c != 0 {
			return c > 0
		}
		return rs[i].PublishedAt.After(rs[j].PublishedAt) // e.g. commit-hash versions
	})
	return rs
}

// ---------- Xray download relay ----------

var (
	relayName = regexp.MustCompile(`^Xray-linux-(64|32|arm64-v8a|arm32-v7a)\.zip(\.dgst)?$`)
	relayMu   sync.Mutex
)

// XrayRelayFile returns a cached copy of a release asset, fetching it from
// GitHub on first use. Only versions known to the panel are relayed.
func (p *Panel) XrayRelayFile(ver, name string) (string, error) {
	ver = strings.TrimPrefix(ver, "v")
	if !relayName.MatchString(name) {
		return "", errors.New("unknown asset")
	}
	var n int64
	p.St.DB.Model(&store.Release{}).Where("component = ? AND version = ?", "xray", ver).Count(&n)
	if n == 0 {
		return "", errors.New("unknown version")
	}
	dir := filepath.Join(p.DataDir, "cache", "xray", "v"+ver)
	path := filepath.Join(dir, name)
	relayMu.Lock()
	defer relayMu.Unlock()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/v%s/%s", ver, name)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GitHub HTTP %d", resp.StatusCode)
	}
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20)); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	f.Close()
	return path, os.Rename(tmp, path)
}

// PruneRelayCache keeps the newest 3 cached versions.
func (p *Panel) PruneRelayCache() {
	dir := filepath.Join(p.DataDir, "cache", "xray")
	es, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var vers []string
	for _, e := range es {
		vers = append(vers, strings.TrimPrefix(e.Name(), "v"))
	}
	sort.Slice(vers, func(i, j int) bool { return xrayconf.CompareVersion(vers[i], vers[j]) > 0 })
	for i := 3; i < len(vers); i++ {
		os.RemoveAll(filepath.Join(dir, "v"+vers[i]))
	}
}

// ---------- signing ----------

// SignPublicKey parses the configured ed25519 key (base64 SPKI or raw 32 bytes).
func (p *Panel) SignPublicKey() (ed25519.PublicKey, error) {
	s := strings.TrimSpace(p.St.Get("sign_pubkey", ""))
	if s == "" {
		return nil, nil
	}
	return ParseEd25519Public(s)
}

func ParseEd25519Public(s string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, err
	}
	if len(b) == ed25519.PublicKeySize {
		return ed25519.PublicKey(b), nil
	}
	k, err := x509.ParsePKIXPublicKey(b)
	if err != nil {
		return nil, err
	}
	pk, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("不是 ed25519 公钥")
	}
	return pk, nil
}

func verifySig(pub ed25519.PublicKey, data []byte, sigB64 string) error {
	if pub == nil {
		return nil
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil || !ed25519.Verify(pub, data, sig) {
		return errors.New("签名校验失败")
	}
	return nil
}

// ---------- agent releases ----------

var archRe = regexp.MustCompile(`^(amd64|arm64|arm|386)$`)
var verRe = regexp.MustCompile(`^v?[0-9A-Za-z.\-+]{1,40}$`)

type AgentAsset struct {
	SHA256 string `json:"sha256"`
	Sig    string `json:"sig,omitempty"`
	Size   int64  `json:"size"`
}

func (p *Panel) agentDir(ver string) string {
	return filepath.Join(p.DataDir, "agent", "v"+strings.TrimPrefix(ver, "v"))
}

// AddAgentRelease stores one architecture of an agent build. When a signing
// key is configured the signature is required and checked here so agents
// never download a binary that would fail verification.
func (p *Panel) AddAgentRelease(ver, arch string, bin []byte, sig, notes string) (*store.Release, error) {
	ver = strings.TrimPrefix(ver, "v")
	if !verRe.MatchString(ver) || !archRe.MatchString(arch) {
		return nil, errors.New("版本号或架构无效")
	}
	if len(bin) < 1<<20 || string(bin[:4]) != "\x7fELF" {
		return nil, errors.New("不是 Linux 可执行文件")
	}
	pub, err := p.SignPublicKey()
	if err != nil {
		return nil, fmt.Errorf("签名公钥无效：%w", err)
	}
	if pub != nil && sig == "" {
		return nil, errors.New("已配置签名公钥，必须同时上传签名")
	}
	if err := verifySig(pub, bin, sig); err != nil {
		return nil, err
	}
	dir := p.agentDir(ver)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "vpsagent-linux-"+arch), bin, 0o755); err != nil {
		return nil, err
	}
	if sig != "" {
		_ = os.WriteFile(filepath.Join(dir, "vpsagent-linux-"+arch+".sig"), []byte(strings.TrimSpace(sig)), 0o644)
	}
	sum := sha256.Sum256(bin)
	var r store.Release
	if p.St.DB.Where("component = ? AND version = ?", "agent", ver).First(&r).Error != nil {
		r = store.Release{Component: "agent", Version: ver, Channel: "stable", Assets: "{}", PublishedAt: time.Now()}
	}
	assets := map[string]AgentAsset{}
	_ = json.Unmarshal([]byte(r.Assets), &assets)
	assets[arch] = AgentAsset{SHA256: hex.EncodeToString(sum[:]), Sig: strings.TrimSpace(sig), Size: int64(len(bin))}
	b, _ := json.Marshal(assets)
	r.Assets = string(b)
	if notes != "" {
		r.Notes = notes
	}
	if err := p.St.DB.Clauses(clause.OnConflict{UpdateAll: true}).Save(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// RegisterBundledAgent records the agent binaries shipped next to the panel
// (built with the same version stamp) as an agent release, so they show up in
// the update center and can be rolled out without uploading them by hand.
func (p *Panel) RegisterBundledAgent(src string) {
	ver := strings.TrimPrefix(version.Version, "v")
	es, err := os.ReadDir(src)
	if err != nil || !verRe.MatchString(ver) {
		return
	}
	have := map[string]AgentAsset{}
	var r store.Release
	if p.St.DB.Where("component = ? AND version = ?", "agent", ver).First(&r).Error == nil {
		_ = json.Unmarshal([]byte(r.Assets), &have)
	}
	custom := p.St.Get("agent_published", "") == ver // a hand-published release with the same name wins
	n := 0
	for _, e := range es {
		arch, okk := strings.CutPrefix(e.Name(), "vpsagent-linux-")
		if e.IsDir() || !okk || !archRe.MatchString(arch) {
			continue
		}
		bin, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			continue
		}
		sum := sha256.Sum256(bin)
		if a, exists := have[arch]; exists && (custom || a.SHA256 == hex.EncodeToString(sum[:])) {
			n++
			continue
		}
		sig, _ := os.ReadFile(filepath.Join(src, e.Name()+".sig"))
		if _, err := p.AddAgentRelease(ver, arch, bin, string(sig), "随面板内置"); err != nil {
			log.Printf("bundled agent %s/%s not registered: %v", ver, arch, err)
			continue
		}
		n++
	}
	if n > 0 {
		_ = p.St.Set("agent_bundled", ver)
	}
}

// AgentCurrent is the version /dl/agent serves: the hand-published release,
// or the build bundled with the panel when none was published.
func (p *Panel) AgentCurrent() string {
	if v := p.St.Get("agent_published", ""); v != "" {
		return v
	}
	return p.St.Get("agent_bundled", "")
}

// PublishAgent makes a version the one served by /dl/agent (new installs).
func (p *Panel) PublishAgent(ver string) error {
	ver = strings.TrimPrefix(ver, "v")
	src := p.agentDir(ver)
	es, err := os.ReadDir(src)
	if err != nil {
		return errors.New("版本不存在")
	}
	dst := filepath.Join(p.DataDir, "agent")
	for _, e := range es {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()+".tmp"), b, 0o755); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(dst, e.Name()+".tmp"), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return p.St.Set("agent_published", ver)
}

func (p *Panel) DeleteAgentRelease(ver string) error {
	ver = strings.TrimPrefix(ver, "v")
	if p.AgentCurrent() == ver {
		return errors.New("不能删除当前发布的版本")
	}
	os.RemoveAll(p.agentDir(ver))
	return p.St.DB.Where("component = ? AND version = ?", "agent", ver).Delete(&store.Release{}).Error
}

// AgentReleaseFile serves /dl/agentv/<ver>/<file>.
func (p *Panel) AgentReleaseFile(ver, file string) (string, bool) {
	if !regexp.MustCompile(`^vpsagent-linux-(amd64|arm64|arm|386)(\.sig)?$`).MatchString(file) || !verRe.MatchString(ver) {
		return "", false
	}
	path := filepath.Join(p.agentDir(ver), file)
	_, err := os.Stat(path)
	return path, err == nil
}

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
