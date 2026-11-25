package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// SSHInstallInput carries one-time credentials; they are never stored.
type SSHInstallInput struct {
	Host               string `json:"host"`
	Port               int    `json:"port"`
	User               string `json:"user"`
	Password           string `json:"password"`
	Key                string `json:"key"` // PEM private key
	Mode               string `json:"mode"`
	HostKeyFingerprint string `json:"host_key_fingerprint"`
}

// SSHInstall logs into the VPS once and runs the install command. Progress
// is a panel-side task ("ssh_install") whose output streams like any other.
func (p *Panel) SSHInstall(serverID uint, in SSHInstallInput) (uint64, error) {
	if net.ParseIP(in.Host) == nil && !strings.Contains(in.Host, ".") {
		return 0, errors.New("主机地址无效")
	}
	if in.Port == 0 {
		in.Port = 22
	}
	if in.User == "" {
		in.User = "root"
	}
	var auth []ssh.AuthMethod
	if strings.TrimSpace(in.Key) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(in.Key))
		if err != nil {
			return 0, errors.New("私钥无效（暂不支持带口令的私钥）")
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if in.Password != "" {
		auth = append(auth, ssh.Password(in.Password), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range ans {
				ans[i] = in.Password
			}
			return ans, nil
		}))
	}
	if len(auth) == 0 {
		return 0, errors.New("请填写密码或私钥")
	}
	mode := in.Mode
	if mode != "node" {
		mode = "probe"
	}
	token, err := p.NewInstallToken(serverID)
	if err != nil {
		return 0, err
	}
	cmd := p.InstallCommand(token, mode)
	if in.User != "root" {
		cmd = "sudo -n bash -c " + shellQuote(cmd)
	} else {
		cmd = "bash -c " + shellQuote(cmd)
	}
	argsJSON, _ := json.Marshal(map[string]string{"host": in.Host})
	t := &store.Task{ID: nextTaskID(), ServerID: serverID, Type: "ssh_install", Args: string(argsJSON), Status: "running", Timeout: 900, CreatedAt: time.Now()}
	if err := p.St.DB.Create(t).Error; err != nil {
		return 0, err
	}
	go p.runSSHInstall(t, in, auth, cmd)
	return t.ID, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'" }

func verifyHostKeyFingerprint(want, keyType, b64SHA256 string) error {
	want = strings.TrimSpace(want)
	if want == "" {
		return nil
	}
	actual := keyType + " SHA256:" + b64SHA256
	normWant := want
	if i := strings.Index(strings.ToUpper(normWant), "SHA256:"); i >= 0 {
		normWant = normWant[i+len("SHA256:"):]
	}
	normWant = strings.TrimRight(strings.TrimSpace(normWant), "=")
	if want == actual || strings.EqualFold(want, "SHA256:"+b64SHA256) || normWant == b64SHA256 {
		return nil
	}
	return fmt.Errorf("主机密钥指纹不匹配：期望 %s，实际 %s", want, actual)
}

// sshOut forwards ssh output as task results (line-buffered).
type sshOut struct {
	mu  sync.Mutex
	buf bytes.Buffer
	p   *Panel
	id  uint64
	sid uint
}

func (w *sshOut) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(b)
	if i := bytes.LastIndexByte(w.buf.Bytes(), '\n'); i >= 0 {
		chunk := string(w.buf.Next(i + 1))
		w.p.onTaskResult(w.sid, &proto.TaskResult{TaskID: w.id, Status: "running", Output: chunk})
	}
	return len(b), nil
}

func (p *Panel) runSSHInstall(t *store.Task, in SSHInstallInput, auth []ssh.AuthMethod, cmd string) {
	out := &sshOut{p: p, id: t.ID, sid: t.ServerID}
	finish := func(err error) {
		out.mu.Lock()
		rest := out.buf.String()
		out.mu.Unlock()
		st, msg := "success", rest
		if err != nil {
			st, msg = "failed", rest+"\n"+err.Error()+"\n"
		}
		p.onTaskResult(t.ServerID, &proto.TaskResult{TaskID: t.ID, Status: st, Output: msg, Finished: true})
	}
	var fp string
	cfg := &ssh.ClientConfig{
		User: in.User, Auth: auth, Timeout: 15 * time.Second,
		// First contact: there is no known host key yet. Show the fingerprint
		// so it can be compared with the provider's console.
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			sum := sha256.Sum256(key.Marshal())
			b64 := strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
			fp = key.Type() + " SHA256:" + b64
			if err := verifyHostKeyFingerprint(in.HostKeyFingerprint, key.Type(), b64); err != nil {
				return err
			}
			return nil
		},
	}
	addr := net.JoinHostPort(in.Host, strconv.Itoa(in.Port))
	_, _ = out.Write([]byte("连接 " + addr + " …\n"))
	cl, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		finish(fmt.Errorf("SSH 连接失败：%w", err))
		return
	}
	defer cl.Close()
	_, _ = out.Write([]byte("已连接，主机密钥 " + fp + "\n"))
	sess, err := cl.NewSession()
	if err != nil {
		finish(err)
		return
	}
	defer sess.Close()
	sess.Stdout, sess.Stderr = out, out
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()
	select {
	case err = <-done:
	case <-ctx.Done():
		err = errors.New("安装超时")
	}
	if err == nil {
		_, _ = out.Write([]byte("安装脚本执行完毕，等待 Agent 连接面板…\n"))
	}
	finish(err)
}
