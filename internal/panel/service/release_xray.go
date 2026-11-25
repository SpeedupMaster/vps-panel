package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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
		if utf8.RuneCountInString(notes) > 8000 {
			notes = truncate(notes, 8000) + "…"
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

var relayName = regexp.MustCompile(`^Xray-linux-(64|32|arm64-v8a|arm32-v7a)\.zip(\.dgst)?$`)

func (p *Panel) relayKeyLock(key string) *sync.Mutex {
	p.relayMu.Lock()
	defer p.relayMu.Unlock()
	if p.relayLocks == nil {
		p.relayLocks = map[string]*sync.Mutex{}
	}
	m := p.relayLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		p.relayLocks[key] = m
	}
	return m
}

// XrayRelayFile returns a cached copy of a release asset, fetching it from
// GitHub on first use. Only versions known to the panel are relayed.
// Cached files are served lock-free; cache misses deduplicate per (ver, name).
func (p *Panel) XrayRelayFile(ver, name string) (string, error) {
	ver = strings.TrimPrefix(ver, "v")
	if !relayName.MatchString(name) {
		return "", errors.New("unknown asset")
	}
	var n int64
	p.St.R().Model(&store.Release{}).Where("component = ? AND version = ?", "xray", ver).Count(&n)
	if n == 0 {
		return "", errors.New("unknown version")
	}
	dir := filepath.Join(p.DataDir, "cache", "xray", "v"+ver)
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	lock := p.relayKeyLock(ver + "/" + name)
	lock.Lock()
	defer lock.Unlock()
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	url := fmt.Sprintf("https://github.com/XTLS/Xray-core/releases/download/v%s/%s", ver, name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
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
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
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
