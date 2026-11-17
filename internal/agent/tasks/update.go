package tasks

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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vpspanel/internal/agent/config"
)

type pendingUpdate struct {
	Binary  string `json:"binary"`
	Backup  string `json:"backup"`
	Version string `json:"version"`
	At      int64  `json:"at"`
}

func pendingPath() string { return filepath.Join(config.DataDir, "update-pending.json") }

// loadSignKey returns the pinned release key (written by install.sh), or nil.
func loadSignKey() (ed25519.PublicKey, error) {
	b, err := os.ReadFile(filepath.Join(config.Dir, "sign.pub"))
	if err != nil {
		return nil, nil
	}
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil {
		return nil, fmt.Errorf("sign.pub 格式错误：%w", err)
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	pk, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("sign.pub 不是 ed25519 公钥")
	}
	return pk, nil
}

func download(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("下载 %s 返回 %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// UpdateAgent replaces the running binary.
//
//	args["url"]     binary URL (served by the panel)
//	args["sha256"]  expected hex digest
//	args["sig"]     base64 ed25519 signature over the binary
//	args["version"] for logging
//
// The old binary is kept; a guard restores it unless the new agent reconnects
// to the panel within 60 seconds (see ConfirmUpdate).
func UpdateAgent(ctx context.Context, args map[string]string, out func(string)) error {
	out("下载 " + args["url"] + "\n")
	bin, err := download(ctx, args["url"], 100<<20)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(bin)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), args["sha256"]) {
		return errors.New("sha256 不匹配")
	}
	out("sha256 校验通过\n")
	pk, err := loadSignKey()
	if err != nil {
		return err
	}
	if pk != nil {
		sig, err := base64.StdEncoding.DecodeString(args["sig"])
		if err != nil || !ed25519.Verify(pk, bin, sig) {
			return errors.New("ed25519 签名校验失败，拒绝更新")
		}
		out("签名校验通过\n")
	} else {
		out("本机未固定签名公钥，跳过签名校验\n")
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	tmp := self + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return err
	}
	if v, err := exec.CommandContext(ctx, tmp, "version").Output(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("新版本无法运行：%v", err)
	} else {
		out("新版本 " + strings.TrimSpace(string(v)) + "\n")
	}
	backup := self + ".old"
	if err := copyFile(self, backup); err != nil {
		return err
	}
	if err := os.Rename(tmp, self); err != nil {
		return err
	}
	p := pendingUpdate{Binary: self, Backup: backup, Version: args["version"], At: time.Now().Unix()}
	b, _ := json.Marshal(p)
	if err := os.WriteFile(pendingPath(), b, 0o600); err != nil {
		return err
	}
	armGuard(p)
	out("已替换，Agent 即将重启；60 秒内连不回面板会自动回滚\n")
	go func() {
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
	return nil
}

// armGuard schedules a rollback outside of this process (the service restart
// kills our process group, so it must not be a child).
func armGuard(p pendingUpdate) {
	script := fmt.Sprintf(`sleep 75; if [ -f %q ]; then cp -f %q %q && rm -f %q; (systemctl restart vpsagent || rc-service vpsagent restart) >/dev/null 2>&1; fi`,
		pendingPath(), p.Backup, p.Binary, pendingPath())
	if _, err := exec.LookPath("systemd-run"); err == nil {
		if err := exec.Command("systemd-run", "--unit=vpsagent-rollback-"+fmt.Sprint(p.At), "--collect", "/bin/sh", "-c", script).Run(); err == nil {
			return
		}
	}
	cmd := exec.Command("setsid", "/bin/sh", "-c", script)
	_ = cmd.Start()
}

// ConfirmUpdate is called once the agent has talked to the panel; it disarms
// the rollback guard.
func ConfirmUpdate() {
	b, err := os.ReadFile(pendingPath())
	if err != nil {
		return
	}
	var p pendingUpdate
	_ = json.Unmarshal(b, &p)
	_ = os.Remove(pendingPath())
	log.Printf("update to %s confirmed", p.Version)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	o, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(o, in); err != nil {
		o.Close()
		return err
	}
	return o.Close()
}
