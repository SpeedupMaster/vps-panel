package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"vpspanel/internal/panel/auth"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
)

// signCommand handles `vps-panel keygen` and `vps-panel sign`. The private
// key is meant to stay offline; only the public key goes into the panel
// settings (sign_pubkey) and onto agents (/etc/vpsagent/sign.pub).
func signCommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "reset-admin":
		return true, resetAdmin(args[1:])
	case "keygen":
		out := "sign.key"
		if len(args) > 1 {
			out = args[1]
		}
		if _, err := os.Stat(out); err == nil {
			return true, fmt.Errorf("%s 已存在", out)
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return true, err
		}
		der, _ := x509.MarshalPKCS8PrivateKey(priv)
		if err := os.WriteFile(out, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
			return true, err
		}
		spki, _ := x509.MarshalPKIXPublicKey(pub)
		fmt.Printf("私钥已写入 %s（请离线保存）\n公钥（填到 设置 → sign_pubkey）：\n%s\n", out, base64.StdEncoding.EncodeToString(spki))
		return true, nil
	case "sign":
		if len(args) < 3 {
			return true, errors.New("用法：vps-panel sign <私钥文件> <二进制文件>...（生成 <文件>.sig）")
		}
		b, err := os.ReadFile(args[1])
		if err != nil {
			return true, err
		}
		blk, _ := pem.Decode(b)
		if blk == nil {
			return true, errors.New("私钥格式无效")
		}
		k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
		if err != nil {
			return true, err
		}
		priv, ok := k.(ed25519.PrivateKey)
		if !ok {
			return true, errors.New("不是 ed25519 私钥")
		}
		for _, f := range args[2:] {
			data, err := os.ReadFile(f)
			if err != nil {
				return true, err
			}
			sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, data))
			if err := os.WriteFile(f+".sig", []byte(sig+"\n"), 0o644); err != nil {
				return true, err
			}
			fmt.Printf("%s.sig  %s…\n", f, strings.TrimSpace(sig)[:16])
		}
		return true, nil
	}
	return false, nil
}

// resetAdmin: vps-panel reset-admin [-data dir]
func resetAdmin(args []string) error {
	dir := env("VPSPANEL_DATA", "./data")
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-data" || args[i] == "--data" {
			dir = args[i+1]
		}
	}
	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	defer st.Close()
	box, err := secret.Load(dir)
	if err != nil {
		return err
	}
	pw, err := auth.New(st, box).ResetAdmin()
	if err != nil {
		return err
	}
	fmt.Printf("管理员密码已重置为：%s\n两步验证已关闭，所有会话已失效。登录后请立即修改密码并重新开启两步验证。\n", pw)
	return nil
}
