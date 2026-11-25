package service

import (
	"bytes"
	"compress/gzip"
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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm/clause"

	"vpspanel/internal/common/version"
	"vpspanel/internal/panel/store"
)

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
var agentFileRe = regexp.MustCompile(`^vpsagent-linux-(amd64|arm64|arm|386)(\.sig)?$`)

type AgentAsset struct {
	SHA256 string `json:"sha256"`
	Sig    string `json:"sig,omitempty"`
	Size   int64  `json:"size"`
}

func (p *Panel) agentDir(ver string) string {
	return filepath.Join(p.DataDir, "agent", "v"+strings.TrimPrefix(ver, "v"))
}

func (p *Panel) bundledDir() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.bundledAgentDir
}

// inspectAgentFile streams an ELF (plain or .gz) to validate its header and
// compute the uncompressed SHA256 and byte size without loading it into memory.
func inspectAgentFile(path string) (shaHex string, size int64, isELF bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false, err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return "", 0, false, err
		}
		defer gr.Close()
		r = gr
	}
	var head [4]byte
	nHead, err := io.ReadFull(r, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, false, err
	}
	h := sha256.New()
	_, _ = h.Write(head[:nHead])
	nRest, err := io.Copy(h, r)
	if err != nil {
		return "", 0, false, err
	}
	return hex.EncodeToString(h.Sum(nil)), int64(nHead) + nRest, nHead == 4 && string(head[:]) == "\x7fELF", nil
}

func verifyAgentFileSig(pub ed25519.PublicKey, path, sigB64 string) error {
	if pub == nil {
		return nil
	}
	if sigB64 == "" {
		return errors.New("已配置签名公钥，必须同时上传签名")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gr, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gr.Close()
		r = gr
	}
	bin, err := io.ReadAll(io.LimitReader(r, 100<<20))
	if err != nil {
		return err
	}
	return verifySig(pub, bin, sigB64)
}

func streamCopyAtomic(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (p *Panel) upsertAgentRelease(ver, arch, shaHex string, size int64, sig, notes string) (*store.Release, error) {
	var r store.Release
	if p.St.DB.Where("component = ? AND version = ?", "agent", ver).First(&r).Error != nil {
		r = store.Release{Component: "agent", Version: ver, Channel: "stable", Assets: "{}", PublishedAt: time.Now()}
	}
	assets := map[string]AgentAsset{}
	_ = json.Unmarshal([]byte(r.Assets), &assets)
	assets[arch] = AgentAsset{SHA256: shaHex, Sig: strings.TrimSpace(sig), Size: size}
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

// AddAgentRelease stores one architecture of an agent build.
func (p *Panel) AddAgentRelease(ver, arch string, bin []byte, sig, notes string) (*store.Release, error) {
	return p.AddAgentReleaseFromReader(ver, arch, bytes.NewReader(bin), sig, notes)
}

// AddAgentReleaseFromReader streams an uploaded agent binary to disk while
// validating ELF magic, size, SHA256, and optional ed25519 signature.
func (p *Panel) AddAgentReleaseFromReader(ver, arch string, src io.Reader, sig, notes string) (*store.Release, error) {
	ver = strings.TrimPrefix(ver, "v")
	if !verRe.MatchString(ver) || !archRe.MatchString(arch) {
		return nil, errors.New("版本号或架构无效")
	}
	pub, err := p.SignPublicKey()
	if err != nil {
		return nil, fmt.Errorf("签名公钥无效：%w", err)
	}
	if pub != nil && sig == "" {
		return nil, errors.New("已配置签名公钥，必须同时上传签名")
	}
	dir := p.agentDir(ver)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, "vpsagent-linux-"+arch)
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return nil, err
	}
	cleanupTmp := func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}
	var head [4]byte
	nHead, err := io.ReadFull(src, head[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		cleanupTmp()
		return nil, err
	}
	if nHead < 4 || string(head[:]) != "\x7fELF" {
		cleanupTmp()
		return nil, errors.New("不是 Linux 可执行文件")
	}
	h := sha256.New()
	if _, err := io.MultiWriter(f, h).Write(head[:nHead]); err != nil {
		cleanupTmp()
		return nil, err
	}
	nRest, err := io.Copy(io.MultiWriter(f, h), src)
	if err != nil {
		cleanupTmp()
		return nil, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	size := int64(nHead) + nRest
	if size < 1<<20 {
		_ = os.Remove(tmp)
		return nil, errors.New("不是 Linux 可执行文件")
	}
	if pub != nil {
		if err := verifyAgentFileSig(pub, tmp, sig); err != nil {
			_ = os.Remove(tmp)
			return nil, err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	_ = os.Remove(dst + ".gz")
	if sig != "" {
		_ = os.WriteFile(dst+".sig", []byte(strings.TrimSpace(sig)), 0o644)
	}
	return p.upsertAgentRelease(ver, arch, hex.EncodeToString(h.Sum(nil)), size, sig, notes)
}

// RegisterBundledAgent records the agent binaries shipped next to the panel
// (plain or .gz, built with the same version stamp) as an agent release,
// serving them directly from src without duplicating them into DataDir.
func (p *Panel) RegisterBundledAgent(src string) {
	p.mu.Lock()
	p.bundledAgentDir = src
	p.mu.Unlock()

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
	custom := p.St.Get("agent_published", "") == ver
	pub, pubErr := p.SignPublicKey()

	candidates := map[string]string{}
	for _, e := range es {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		base := strings.TrimSuffix(name, ".gz")
		arch, okk := strings.CutPrefix(base, "vpsagent-linux-")
		if !okk || !archRe.MatchString(arch) {
			continue
		}
		if _, exists := candidates[arch]; !exists || !strings.HasSuffix(name, ".gz") {
			candidates[arch] = filepath.Join(src, name)
		}
	}

	n := 0
	for arch, path := range candidates {
		shaHex, size, isELF, err := inspectAgentFile(path)
		if err != nil {
			continue
		}
		if a, exists := have[arch]; exists && (custom || a.SHA256 == shaHex) {
			n++
			continue
		}
		if !isELF || size < 1<<20 {
			log.Printf("bundled agent %s/%s not registered: 不是 Linux 可执行文件", ver, arch)
			continue
		}
		if pubErr != nil {
			log.Printf("bundled agent %s/%s not registered: 签名公钥无效：%v", ver, arch, pubErr)
			continue
		}
		sig, _ := os.ReadFile(filepath.Join(src, "vpsagent-linux-"+arch+".sig"))
		if err := verifyAgentFileSig(pub, path, string(sig)); err != nil {
			log.Printf("bundled agent %s/%s not registered: %v", ver, arch, err)
			continue
		}
		if _, err := p.upsertAgentRelease(ver, arch, shaHex, size, string(sig), "随面板内置"); err != nil {
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
	if err != nil && ver == p.St.Get("agent_bundled", "") {
		if bd := p.bundledDir(); bd != "" {
			src = bd
			es, err = os.ReadDir(src)
		}
	}
	if err != nil {
		return errors.New("版本不存在")
	}
	dst := filepath.Join(p.DataDir, "agent")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range es {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "vpsagent-linux-") {
			continue
		}
		mode := os.FileMode(0o755)
		if strings.HasSuffix(e.Name(), ".sig") || strings.HasSuffix(e.Name(), ".gz") {
			mode = 0o644
		}
		if err := streamCopyAtomic(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), mode); err != nil {
			return err
		}
		if strings.HasSuffix(e.Name(), ".gz") {
			_ = os.Remove(filepath.Join(dst, strings.TrimSuffix(e.Name(), ".gz")))
		} else if !strings.HasSuffix(e.Name(), ".sig") {
			_ = os.Remove(filepath.Join(dst, e.Name()+".gz"))
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

func findAgentFileInDir(dir, file string) (string, bool) {
	if dir == "" {
		return "", false
	}
	path := filepath.Join(dir, file)
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	if !strings.HasSuffix(file, ".sig") {
		gz := path + ".gz"
		if _, err := os.Stat(gz); err == nil {
			return gz, true
		}
	}
	return "", false
}

// AgentReleaseFile serves /dl/agentv/<ver>/<file>, returning either the plain
// binary or its .gz counterpart.
func (p *Panel) AgentReleaseFile(ver, file string) (string, bool) {
	if !agentFileRe.MatchString(file) || !verRe.MatchString(ver) {
		return "", false
	}
	if path, ok := findAgentFileInDir(p.agentDir(ver), file); ok {
		return path, true
	}
	if ver == p.St.Get("agent_bundled", "") {
		return findAgentFileInDir(p.bundledDir(), file)
	}
	return "", false
}

// ResolveAgentFile finds the active binary (or .sig) for /dl/agent/.
func (p *Panel) ResolveAgentFile(file string) (string, bool) {
	dataAgentDir := filepath.Join(p.DataDir, "agent")
	bundled := p.bundledDir()
	if p.St.Get("agent_published", "") == "" {
		if path, ok := findAgentFileInDir(bundled, file); ok {
			return path, true
		}
		return findAgentFileInDir(dataAgentDir, file)
	}
	if path, ok := findAgentFileInDir(dataAgentDir, file); ok {
		return path, true
	}
	return findAgentFileInDir(bundled, file)
}
