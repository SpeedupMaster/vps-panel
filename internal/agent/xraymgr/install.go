package xraymgr

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// assetName maps GOARCH to Xray's release file name.
func assetName() string {
	switch runtime.GOARCH {
	case "arm64":
		return "Xray-linux-arm64-v8a.zip"
	case "arm":
		return "Xray-linux-arm32-v7a.zip"
	case "386":
		return "Xray-linux-32.zip"
	default:
		return "Xray-linux-64.zip"
	}
}

func fetch(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vpsagent")
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// LatestVersion asks GitHub for the newest Xray release tag (without "v").
func LatestVersion(ctx context.Context) (string, error) {
	b, err := fetch(ctx, "https://api.github.com/repos/XTLS/Xray-core/releases/latest", 1<<20)
	if err != nil {
		return "", err
	}
	var r struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(b, &r); err != nil || r.TagName == "" {
		return "", errors.New("无法解析最新版本")
	}
	return strings.TrimPrefix(r.TagName, "v"), nil
}

// Source describes where to download a release from. BaseURL defaults to
// GitHub; the panel may pass its own relay (".../dl/xray/v25.1.1/").
type Source struct {
	Version string
	BaseURL string
}

func (s Source) url(file string) string {
	if s.BaseURL != "" {
		return strings.TrimSuffix(s.BaseURL, "/") + "/" + file
	}
	return fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/v%s/%s", s.Version, file)
}

// download fetches and verifies the release zip against its .dgst file.
func download(ctx context.Context, src Source, out func(string)) (*zip.Reader, error) {
	name := assetName()
	out("下载 " + src.url(name) + "\n")
	data, err := fetch(ctx, src.url(name), 200<<20)
	if err != nil {
		return nil, err
	}
	dg, err := fetch(ctx, src.url(name+".dgst"), 1<<16)
	if err != nil {
		return nil, fmt.Errorf("下载校验文件失败：%w", err)
	}
	sum := sha256.Sum256(data)
	want := ""
	for _, l := range strings.Split(string(dg), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "SHA2-256" {
			want = strings.TrimSpace(v)
		}
	}
	if want == "" || !strings.EqualFold(want, hex.EncodeToString(sum[:])) {
		return nil, errors.New(".dgst 校验失败")
	}
	out("SHA2-256 校验通过\n")
	return zip.NewReader(bytes.NewReader(data), int64(len(data)))
}

func extract(zr *zip.Reader, name, dst string, mode os.FileMode) error {
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		tmp := dst + ".tmp"
		w, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, io.LimitReader(rc, 300<<20)); err != nil {
			w.Close()
			return err
		}
		w.Close()
		return os.Rename(tmp, dst)
	}
	return fmt.Errorf("压缩包里没有 %s", name)
}

// Install downloads Xray, registers the service and writes a placeholder
// config (the panel pushes the real one right after).
func (m *Manager) Install(ctx context.Context, src Source, out func(string)) error {
	m.busy.Lock()
	defer m.busy.Unlock()
	if src.Version == "" || src.Version == "latest" {
		v, err := LatestVersion(ctx)
		if err != nil {
			return err
		}
		src.Version = v
	}
	zr, err := download(ctx, src, out)
	if err != nil {
		return err
	}
	for _, d := range []string{filepath.Dir(Bin), ConfDir, AssetDir} {
		_ = os.MkdirAll(d, 0o755)
	}
	if err := extract(zr, "xray", Bin, 0o755); err != nil {
		return err
	}
	for _, f := range []string{"geoip.dat", "geosite.dat"} {
		if err := extract(zr, f, filepath.Join(AssetDir, f), 0o644); err != nil {
			out("警告：" + err.Error() + "\n")
		}
	}
	if _, err := os.Stat(confPath()); err != nil {
		placeholder := fmt.Sprintf(`{"log":{"loglevel":"warning"},"api":{"tag":"api","services":["HandlerService","StatsService"]},"stats":{},"policy":{"levels":{"0":{"statsUserUplink":true,"statsUserDownlink":true}},"system":{"statsInboundUplink":true,"statsInboundDownlink":true,"statsOutboundUplink":true,"statsOutboundDownlink":true}},"inbounds":[{"tag":"api","listen":"127.0.0.1","port":%s,"protocol":"dokodemo-door","settings":{"address":"127.0.0.1"}}],"outbounds":[{"tag":"direct","protocol":"freedom"}],"routing":{"rules":[{"inboundTag":["api"],"outboundTag":"api"}]}}`,
			APIAddr[strings.LastIndex(APIAddr, ":")+1:])
		_ = os.WriteFile(confPath(), pretty([]byte(placeholder)), 0o600)
	}
	if err := installService(); err != nil {
		return fmt.Errorf("注册服务失败：%w", err)
	}
	if err := RestartService(ctx); err != nil {
		return fmt.Errorf("启动失败：%s", RecentLog(ctx, 10))
	}
	m.mu.Lock()
	m.version = ""
	m.mu.Unlock()
	out("Xray " + m.Version() + " 安装完成\n")
	return nil
}

// Update swaps the binary after checking the current config still passes
// `xray run -test` with the new version; it rolls back if Xray won't start.
func (m *Manager) Update(ctx context.Context, src Source, out func(string)) error {
	m.busy.Lock()
	defer m.busy.Unlock()
	if !Installed() {
		return errors.New("Xray 未安装")
	}
	if src.Version == "" || src.Version == "latest" {
		v, err := LatestVersion(ctx)
		if err != nil {
			return err
		}
		src.Version = v
	}
	old := m.Version()
	if old == src.Version {
		out("已经是 " + old + "\n")
		return nil
	}
	zr, err := download(ctx, src, out)
	if err != nil {
		return err
	}
	newBin := Bin + ".new"
	if err := extract(zr, "xray", newBin, 0o755); err != nil {
		return err
	}
	out("用新版本校验现有配置\n")
	s, terr := m.test(ctx, newBin, confPath())
	if terr != nil {
		os.Remove(newBin)
		return fmt.Errorf("新版本不兼容当前配置，未更新：%s", lastLines(s, 6))
	}
	backup := Bin + ".old"
	_ = os.Rename(Bin, backup)
	if err := os.Rename(newBin, Bin); err != nil {
		_ = os.Rename(backup, Bin)
		return err
	}
	m.mu.Lock()
	m.version = ""
	m.mu.Unlock()
	if err := RestartService(ctx); err != nil || !waitActive(ctx, 5*time.Second) {
		out("新版本启动失败，回滚到 " + old + "\n")
		_ = os.Rename(backup, Bin)
		_ = RestartService(ctx)
		return errors.New("健康检查失败，已回滚")
	}
	out(fmt.Sprintf("Xray %s → %s\n", old, m.Version()))
	return nil
}

func (m *Manager) Uninstall(ctx context.Context, out func(string)) error {
	m.busy.Lock()
	defer m.busy.Unlock()
	removeService()
	for _, p := range []string{Bin, Bin + ".old", ConfDir, AssetDir} {
		_ = os.RemoveAll(p)
	}
	_ = os.Remove(m.statePath())
	out("Xray 已卸载\n")
	return nil
}

// Geo sources: official (bundled with Xray) or Loyalsoldier's enhanced rules.
var GeoSources = map[string][2]string{
	"loyalsoldier": {
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat",
		"https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat",
	},
	"official": {
		"https://github.com/v2fly/geoip/releases/latest/download/geoip.dat",
		"https://github.com/v2fly/domain-list-community/releases/latest/download/dlc.dat",
	},
}

// UpdateGeo refreshes geoip/geosite. Each file is checked against its
// .sha256sum and the config is re-tested before Xray restarts.
func (m *Manager) UpdateGeo(ctx context.Context, source string, out func(string)) error {
	m.busy.Lock()
	defer m.busy.Unlock()
	urls, ok := GeoSources[source]
	if !ok {
		urls = GeoSources["loyalsoldier"]
	}
	names := []string{"geoip.dat", "geosite.dat"}
	var backups []string
	for i, u := range urls {
		data, err := fetch(ctx, u, 100<<20)
		if err != nil {
			return err
		}
		if sumFile, err := fetch(ctx, u+".sha256sum", 4096); err == nil {
			sum := sha256.Sum256(data)
			if f := strings.Fields(string(sumFile)); len(f) == 0 || !strings.EqualFold(f[0], hex.EncodeToString(sum[:])) {
				return fmt.Errorf("%s 校验失败", names[i])
			}
		}
		dst := filepath.Join(AssetDir, names[i])
		_ = os.Rename(dst, dst+".bak")
		backups = append(backups, dst)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
		out(fmt.Sprintf("%s %.1f MB\n", names[i], float64(len(data))/1e6))
	}
	restore := func() {
		for _, b := range backups {
			_ = os.Rename(b+".bak", b)
		}
	}
	if s, err := m.test(ctx, Bin, confPath()); err != nil {
		restore()
		return fmt.Errorf("新规则文件校验失败，已还原：%s", lastLines(s, 5))
	}
	if ServiceActive() {
		if err := RestartService(ctx); err != nil || !waitActive(ctx, 5*time.Second) {
			restore()
			_ = RestartService(ctx)
			return errors.New("重启失败，已还原规则文件")
		}
	}
	out("规则文件已更新\n")
	return nil
}
