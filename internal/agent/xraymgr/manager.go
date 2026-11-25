// Package xraymgr installs, configures and supervises Xray on node machines.
// It talks to Xray through the `xray api` CLI, so the agent does not link
// xray-core.
package xraymgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpspanel/internal/agent/firewall"
	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/xrayconf"
)

var (
	Bin      = "/usr/local/bin/xray"
	ConfDir  = "/usr/local/etc/xray"
	AssetDir = "/usr/local/share/xray"
	APIAddr  = "127.0.0.1:10085"

	// Overridable in unit tests.
	firewallApply = firewall.Apply
	serviceActive = ServiceActive
	runXrayAPI    = func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, Bin, args...).CombinedOutput()
	}
	testXrayConfig = func(ctx context.Context, bin, path string) (string, error) {
		cmd := exec.CommandContext(ctx, bin, "run", "-test", "-c", path)
		cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+AssetDir)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
)

func confPath() string { return filepath.Join(ConfDir, "config.json") }

type persisted struct {
	Hash    string `json:"hash"`
	Version int    `json:"version"`
}

type Manager struct {
	dataDir string

	mu      sync.Mutex
	st      persisted
	version string
	verTime time.Time
	busy    sync.Mutex // serialises config writes / restarts
	proc    procSampler
	drifted bool
}

func New(dataDir string) *Manager {
	m := &Manager{dataDir: dataDir}
	if b, err := os.ReadFile(m.statePath()); err == nil {
		_ = json.Unmarshal(b, &m.st)
	}
	firewall.Restore() // re-apply the landing whitelist after a reboot
	return m
}

func (m *Manager) statePath() string { return filepath.Join(m.dataDir, "xray-state.json") }

func (m *Manager) saveState(hash string, ver int) {
	m.mu.Lock()
	m.st = persisted{Hash: hash, Version: ver}
	b, _ := json.Marshal(m.st)
	m.mu.Unlock()
	_ = os.WriteFile(m.statePath(), b, 0o600)
}

func hashBytes(b []byte) string { return xrayconf.Hash(b) }

func Installed() bool {
	_, err := os.Stat(Bin)
	return err == nil && serviceInstalled()
}

// Version returns e.g. "25.10.15", cached until the binary changes.
func (m *Manager) Version() string {
	fi, err := os.Stat(Bin)
	if err != nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.version != "" && fi.ModTime().Equal(m.verTime) {
		return m.version
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, Bin, "version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) >= 2 {
		m.version, m.verTime = f[1], fi.ModTime()
	}
	return m.version
}

func (m *Manager) Proc() *proto.XrayProc {
	p := m.proc.sample()
	p.Version = m.Version()
	return p
}

func (m *Manager) State() *proto.XrayState {
	s := &proto.XrayState{Installed: Installed()}
	if !s.Installed {
		return s
	}
	s.Version = m.Version()
	s.Running = ServiceActive()
	m.mu.Lock()
	s.ConfigHash, s.ConfigVer = m.st.Hash, m.st.Version
	m.mu.Unlock()
	if b, err := os.ReadFile(confPath()); err == nil && s.ConfigHash != "" {
		s.Drift = hashBytes(b) != s.ConfigHash
	}
	return s
}

// Stats reads and resets Xray's traffic counters.
func (m *Manager) Stats() []proto.TrafficItem {
	if !Installed() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, Bin, "api", "statsquery", "--server="+APIAddr, "-reset").Output()
	if err != nil {
		return nil
	}
	return parseStats(out)
}

func parseStats(out []byte) []proto.TrafficItem {
	var r struct {
		Stat []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"stat"`
	}
	if json.Unmarshal(out, &r) != nil {
		return nil
	}
	idx := map[string]int{}
	var items []proto.TrafficItem
	for _, s := range r.Stat {
		// user>>>r-12>>>traffic>>>uplink
		p := strings.Split(s.Name, ">>>")
		if len(p) != 4 || p[2] != "traffic" {
			continue
		}
		if p[0] == "outbound" && (p[1] == "api" || p[1] == "block") || p[0] == "inbound" && p[1] == "api" {
			continue
		}
		v, _ := strconv.ParseUint(strings.Trim(string(s.Value), `"`), 10, 64)
		if v == 0 {
			continue
		}
		key := p[0] + "|" + p[1]
		i, ok := idx[key]
		if !ok {
			i = len(items)
			idx[key] = i
			items = append(items, proto.TrafficItem{Scope: p[0], Name: p[1]})
		}
		if p[3] == "uplink" {
			items[i].Up += v
		} else {
			items[i].Down += v
		}
	}
	return items
}

func (m *Manager) test(ctx context.Context, bin, path string) (string, error) {
	return testXrayConfig(ctx, bin, path)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// Apply validates and installs a full config, restarting Xray and rolling
// back to the previous file if it fails to come up.
func (m *Manager) Apply(ctx context.Context, ac *proto.ApplyConfig, out func(string)) error {
	m.busy.Lock()
	defer m.busy.Unlock()
	return m.applyLocked(ctx, ac, out)
}

func (m *Manager) applyLocked(ctx context.Context, ac *proto.ApplyConfig, out func(string)) error {
	if _, err := os.Stat(Bin); err != nil {
		return errors.New("Xray 未安装")
	}
	if ac.Firewall != nil {
		if err := firewallApply(ac.Firewall.Port, ac.Firewall.Allowed); err != nil {
			out("防火墙白名单失败：" + err.Error() + "\n")
			return fmt.Errorf("防火墙白名单应用失败：%w", err)
		}
		if ac.Firewall.Port == 0 {
			out("防火墙：未启用落地白名单\n")
		} else {
			out(fmt.Sprintf("防火墙：端口 %d 仅允许 %d 个入口 IP\n", ac.Firewall.Port, len(ac.Firewall.Allowed)))
		}
	}
	if len(ac.Config) == 0 {
		return nil
	}
	cfg := pretty(ac.Config)
	cur, _ := os.ReadFile(confPath())
	if bytes.Equal(cur, cfg) && serviceActive() && !ac.Restart {
		out("配置未变化\n")
		m.saveState(hashBytes(cfg), ac.Version)
		return nil
	}
	_ = os.MkdirAll(ConfDir, 0o755)
	// Keep the .json extension: newer Xray picks the config format from it
	// and rejects "config.json.new".
	tmp := filepath.Join(ConfDir, "config.new.json")
	if err := os.WriteFile(tmp, cfg, 0o600); err != nil {
		return err
	}
	out("xray run -test 校验配置\n")
	if s, err := m.test(ctx, Bin, tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("配置校验失败：%s", lastLines(s, 8))
	}
	bak := confPath() + ".bak"
	if len(cur) > 0 {
		_ = os.WriteFile(bak, cur, 0o600)
	}
	if err := os.Rename(tmp, confPath()); err != nil {
		return err
	}
	if err := ensureService(); err != nil {
		return fmt.Errorf("注册服务失败：%w", err)
	}
	out("重启 Xray\n")
	if err := RestartService(ctx); err != nil || !waitActive(ctx, 4*time.Second) {
		out("Xray 启动失败，回滚到上一份配置\n")
		detail := startErrDetail(ctx, err)
		if len(cur) > 0 {
			_ = writeFileAtomic(confPath(), cur, 0o600)
			_ = RestartService(ctx)
		}
		return fmt.Errorf("新配置启动失败，已回滚：%s", detail)
	}
	m.saveState(hashBytes(cfg), ac.Version)
	out(fmt.Sprintf("已应用配置 v%d\n", ac.Version))
	return nil
}

// Op hot-adds / removes a user through the API and persists the full config
// so a later restart keeps the change. If the API call fails it falls back to
// a full apply with restart.
func (m *Manager) Op(ctx context.Context, op *proto.XrayOp, out func(string)) error {
	m.busy.Lock()
	defer m.busy.Unlock()
	err := m.hot(ctx, op)
	if err != nil {
		out("热更新失败（" + err.Error() + "），改为重启应用\n")
		if len(op.FullConfig) == 0 {
			return err
		}
		return m.applyLocked(ctx, &proto.ApplyConfig{Version: op.Version, Config: op.FullConfig, Restart: true}, out)
	}
	if len(op.FullConfig) > 0 {
		cfg := pretty(op.FullConfig)
		if err := writeFileAtomic(confPath(), cfg, 0o600); err != nil {
			return err
		}
		m.saveState(hashBytes(cfg), op.Version)
	}
	out(fmt.Sprintf("已通过 API %s %s（无需重启）\n", op.Op, op.User.Email))
	return nil
}

func (m *Manager) hot(ctx context.Context, op *proto.XrayOp) error {
	if op.User == nil {
		return errors.New("缺少用户")
	}
	if !serviceActive() {
		return errors.New("Xray 未运行")
	}
	switch op.Op {
	case "remove_user":
		// Remove user BEFORE routing rule so the user cannot transiently fall
		// through to direct egress while still connected.
		b, err := runXrayAPI(ctx, "api", "rmu", "--server="+APIAddr, "-tag="+op.Tag, op.User.Email)
		if err != nil {
			return cmdErr(err, b)
		}
		if len(op.Rule) > 0 {
			_, _ = runXrayAPI(ctx, "api", "rmrules", "--server="+APIAddr, op.User.Email)
		}
		return nil
	case "add_user":
		in, err := inboundWithUser(op.FullConfig, op.Tag, op.User)
		if err != nil {
			return err
		}
		f, err := os.CreateTemp("", "adu-*.json")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		_, _ = f.Write(in)
		f.Close()
		b, err := runXrayAPI(ctx, "api", "adu", "--server="+APIAddr, f.Name())
		if err != nil || !aduAdded(string(b)) {
			return cmdErr(err, b)
		}
		if len(op.Rule) > 0 {
			rf, err := os.CreateTemp("", "adrules-*.json")
			if err != nil {
				return err
			}
			defer os.Remove(rf.Name())
			fmt.Fprintf(rf, `{"routing":{"rules":[%s]}}`, op.Rule)
			rf.Close()
			b, err := runXrayAPI(ctx, "api", "adrules", "--server="+APIAddr, "-append", rf.Name())
			if err != nil || strings.Contains(string(b), "failed") {
				// Undo the user so the fallback restart starts clean.
				_, _ = runXrayAPI(ctx, "api", "rmu", "--server="+APIAddr, "-tag="+op.Tag, op.User.Email)
				return fmt.Errorf("添加路由规则失败：%v %s", err, strings.TrimSpace(string(b)))
			}
		}
		return nil
	}
	return fmt.Errorf("未知操作 %s", op.Op)
}

var aduRe = regexp.MustCompile(`Added ([0-9]+) user`)

// aduAdded reports whether `xray api adu` really added the user. Xray exits 0
// and prints "Added 0 user(s)" when the inbound cannot take users at runtime,
// e.g. a Shadowsocks 2022 inbound that started without clients runs in
// single-user mode; the caller then falls back to a restart.
func aduAdded(out string) bool {
	if strings.Contains(out, "failed") {
		return false
	}
	m := aduRe.FindStringSubmatch(out)
	return m == nil || m[1] != "0"
}

// inboundWithUser builds the `xray api adu` input: the inbound with the given
// tag from the full config, carrying only the new client.
func inboundWithUser(full []byte, tag string, u *proto.XrayUser) ([]byte, error) {
	var cfg struct {
		Inbounds []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(full, &cfg); err != nil {
		return nil, err
	}
	for _, in := range cfg.Inbounds {
		if in["tag"] != tag {
			continue
		}
		settings, _ := in["settings"].(map[string]any)
		if settings == nil {
			settings = map[string]any{}
		}
		c := map[string]any{"email": u.Email}
		if u.ID != "" {
			c["id"] = u.ID
		}
		if u.Flow != "" {
			c["flow"] = u.Flow
		}
		if u.Password != "" {
			c["password"] = u.Password
		}
		settings["clients"] = []any{c}
		in["settings"] = settings
		return json.Marshal(map[string]any{"inbounds": []any{in}})
	}
	return nil, fmt.Errorf("配置中找不到入站 %s", tag)
}

// Watch reports crashes and local config drift.
func (m *Manager) Watch(ctx context.Context, events chan<- *proto.Event) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	wasRunning := ServiceActive()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !Installed() {
			continue
		}
		running := ServiceActive()
		if wasRunning && !running {
			m.busy.Lock() // not during an intentional restart
			running = ServiceActive()
			m.busy.Unlock()
			if !running {
				events <- &proto.Event{TS: time.Now().Unix(), Kind: "xray_crash", Detail: RecentLog(ctx, 10)}
			}
		} else if !wasRunning && running {
			events <- &proto.Event{TS: time.Now().Unix(), Kind: "xray_restart", Detail: "Xray 已恢复运行"}
		}
		wasRunning = running
		st := m.State()
		if st.Drift && !m.drifted {
			events <- &proto.Event{TS: time.Now().Unix(), Kind: "config_drift", Detail: "本地 config.json 与面板下发版本不一致"}
		}
		m.drifted = st.Drift
	}
}

func pretty(b []byte) []byte { return xrayconf.Pretty(b) }

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

func waitActive(ctx context.Context, d time.Duration) bool {
	deadline := time.Now().Add(d)
	time.Sleep(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if ServiceActive() {
			time.Sleep(time.Second) // still up a moment later?
			return ServiceActive()
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}

// cmdErr reports a failed xray CLI call by its output, plus the exit error
// when there was one.
func cmdErr(err error, out []byte) error {
	msg := strings.TrimSpace(string(out))
	if err != nil {
		return fmt.Errorf("%v %s", err, msg)
	}
	return errors.New(msg)
}
