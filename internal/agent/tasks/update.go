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

// parseSignKey parses either a raw 32-byte base64 Ed25519 public key or a
// 44-byte PKIX SPKI DER base64 Ed25519 public key.
func parseSignKey(s string) (ed25519.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("sign.pub 格式错误：%w", err)
	}
	if len(der) == ed25519.PublicKeySize {
		return ed25519.PublicKey(der), nil
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

// loadSignKey returns the pinned release key (written by install.sh), or nil.
func loadSignKey() (ed25519.PublicKey, error) {
	b, err := os.ReadFile(filepath.Join(config.Dir, "sign.pub"))
	if err != nil {
		return nil, nil
	}
	return parseSignKey(string(b))
}

func downloadToFile(ctx context.Context, url, dst string, max int64) (string, error) {
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
		return "", fmt.Errorf("下载 %s 返回 %d", url, resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, max+1))
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return "", closeErr
	}
	if n > max {
		_ = os.Remove(dst)
		return "", fmt.Errorf("下载 %s 超过大小限制", url)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyFileSig(pk ed25519.PublicKey, path, sigB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return errors.New("ed25519 签名校验失败，拒绝更新")
	}
	bin, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pk, bin, sig) {
		return errors.New("ed25519 签名校验失败，拒绝更新")
	}
	return nil
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
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	tmp := self + ".new"

	out("下载 " + args["url"] + "\n")
	sumHex, err := downloadToFile(ctx, args["url"], tmp, 100<<20)
	if err != nil {
		return err
	}
	if !strings.EqualFold(sumHex, args["sha256"]) {
		_ = os.Remove(tmp)
		return errors.New("sha256 不匹配")
	}
	out("sha256 校验通过\n")
	pk, err := loadSignKey()
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if pk != nil {
		if err := verifyFileSig(pk, tmp, args["sig"]); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		out("签名校验通过\n")
	} else {
		out("本机未固定签名公钥，跳过签名校验\n")
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if v, err := exec.CommandContext(ctx, tmp, "version").Output(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("新版本无法运行：%v", err)
	} else {
		out("新版本 " + strings.TrimSpace(string(v)) + "\n")
	}
	backup := self + ".old"
	if err := copyFile(self, backup); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, self); err != nil {
		_ = os.Remove(tmp)
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

const rollbackWaitSec = 60

func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// rollbackScript builds the detached shell guard. It stages the backup to a
// sibling file and atomically renames it over p.Binary so a running or
// crash-looping new binary never fails rollback with ETXTBSY.
func rollbackScript(p pendingUpdate, pendingFile string) string {
	staged := p.Binary + ".rollback"
	return fmt.Sprintf(
		`trap '' HUP; sleep %d; if [ -f %s ]; then cp -f %s %s && chmod 0755 %s && mv -f %s %s && rm -f %s; (systemctl restart vpsagent || rc-service vpsagent restart) >/dev/null 2>&1; fi`,
		rollbackWaitSec,
		shQuote(pendingFile),
		shQuote(p.Backup), shQuote(staged),
		shQuote(staged),
		shQuote(staged), shQuote(p.Binary),
		shQuote(pendingFile),
	)
}

// armGuard schedules a rollback outside of this process (the service restart
// kills our process group, so it must not be a child).
func armGuard(p pendingUpdate) {
	script := rollbackScript(p, pendingPath())
	if _, err := exec.LookPath("systemd-run"); err == nil {
		if err := exec.Command("systemd-run", "--unit=vpsagent-rollback-"+fmt.Sprint(p.At), "--collect", "/bin/sh", "-c", script).Run(); err == nil {
			return
		}
	}
	// Non-systemd fallback: detach into a new session and ignore SIGHUP with
	// stdio closed so OpenRC / sysvinit service stops do not kill the guard.
	var cmd *exec.Cmd
	switch {
	case have("setsid") && have("nohup"):
		cmd = exec.Command("setsid", "nohup", "/bin/sh", "-c", script)
	case have("setsid"):
		cmd = exec.Command("setsid", "/bin/sh", "-c", script)
	case have("nohup"):
		cmd = exec.Command("nohup", "/bin/sh", "-c", script)
	default:
		cmd = exec.Command("/bin/sh", "-c", script)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
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
