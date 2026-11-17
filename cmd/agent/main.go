// Command vpsagent runs on every VPS.
//
//	vpsagent register --panel URL --token T --mode probe|node
//	vpsagent run
//	vpsagent config set allow_remote_exec true
//	vpsagent version
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"vpspanel/internal/agent"
	"vpspanel/internal/agent/buffer"
	"vpspanel/internal/agent/config"
	"vpspanel/internal/agent/tasks"
	"vpspanel/internal/agent/xraymgr"
	"vpspanel/internal/common/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "register":
		fs := flag.NewFlagSet("register", flag.ExitOnError)
		panel := fs.String("panel", "", "panel URL")
		token := fs.String("token", "", "one-time install token")
		mode := fs.String("mode", "probe", "probe | node")
		_ = fs.Parse(os.Args[2:])
		creds, err := agent.Register(*panel, *token, *mode)
		if err != nil {
			log.Fatalf("注册失败：%v", err)
		}
		if err := config.SaveCredentials(creds); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("已注册为 %s（%s）\n", creds.ServerName, creds.AgentID)
	case "run":
		run()
	case "config":
		configCmd(os.Args[2:])
	case "version", "-v", "--version":
		fmt.Println(version.Version)
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: vpsagent register|run|config|version")
	os.Exit(2)
}

func run() {
	log.SetFlags(log.LstdFlags)
	creds, err := config.LoadCredentials()
	if err != nil {
		log.Fatalf("读取凭证失败（请先执行 vpsagent register）：%v", err)
	}
	local := config.LoadLocal()
	buf, err := buffer.Open(config.DataDir)
	if err != nil {
		log.Fatal(err)
	}
	defer buf.Close()

	a := agent.New(creds, local, buf)
	if local.Mode == "node" {
		a.Xray = xraymgr.New(config.DataDir)
	}
	a.OnConnected = tasks.ConfirmUpdate
	tasks.Register(a)
	log.Printf("vpsagent %s mode=%s remote_exec=%v", version.Version, local.Mode, local.Switches.AllowRemoteExec)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.Run(ctx)
}

// configCmd edits local switches: `vpsagent config set allow_remote_exec true`.
func configCmd(args []string) {
	if len(args) != 3 || args[0] != "set" {
		fmt.Fprintln(os.Stderr, "usage: vpsagent config set <allow_remote_exec|allow_system_tune|allow_self_update> <true|false>")
		os.Exit(2)
	}
	key, val := args[1], args[2]
	switch key {
	case "allow_remote_exec", "allow_system_tune", "allow_self_update":
	default:
		log.Fatalf("未知开关 %s", key)
	}
	if val != "true" && val != "false" {
		log.Fatal("值只能是 true 或 false")
	}
	path := filepath.Join(config.Dir, "agent.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	found := false
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), key+":") {
			lines[i] = key + ": " + val
			found = true
		}
	}
	if !found {
		lines = append(lines, key+": "+val)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s = %s（执行 systemctl restart vpsagent 生效）\n", key, val)
}
