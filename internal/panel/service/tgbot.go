package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/panel/notify"
	"vpspanel/internal/panel/store"
)

// RunTelegramBot long-polls the first enabled Telegram channel that has
// bot_commands=1 and answers read-only commands from its bound chat only.
func (p *Panel) RunTelegramBot(ctx context.Context) {
	var offset int64
	var registered string
	for ctx.Err() == nil {
		cfg := p.botChannel()
		if cfg == nil {
			sleepCtx(ctx, time.Minute)
			continue
		}
		if registered != cfg["bot_token"] {
			if err := notify.SetTelegramCommands(ctx, cfg); err != nil {
				log.Printf("tgbot: setMyCommands: %v", err)
			}
			registered = cfg["bot_token"]
		}
		ups, err := notify.TelegramUpdates(ctx, cfg, offset)
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("tgbot: %v", err)
				sleepCtx(ctx, 30*time.Second)
			}
			continue
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			if u.Message == nil || strconv.FormatInt(u.Message.Chat.ID, 10) != strings.TrimSpace(cfg["chat_id"]) {
				continue // only the bound chat
			}
			reply := p.BotReply(u.Message.Text)
			if reply != "" {
				_ = notify.Telegram(ctx, cfg, reply)
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (p *Panel) botChannel() map[string]string {
	var chs []store.NotifyChannel
	p.St.DB.Where("type = ? AND enabled = ?", "telegram", true).Find(&chs)
	for i := range chs {
		cfg, err := p.ChannelConfig(&chs[i])
		if err == nil && cfg["bot_commands"] == "1" && cfg["bot_token"] != "" && cfg["chat_id"] != "" {
			return cfg
		}
	}
	return nil
}

// BotReply renders the answer to one bot command.
func (p *Panel) BotReply(text string) string {
	cmd, arg, _ := strings.Cut(strings.TrimSpace(text), " ")
	cmd = strings.ToLower(strings.SplitN(cmd, "@", 2)[0])
	arg = strings.TrimSpace(arg)
	var svs []store.Server
	p.St.DB.Order("sort, id").Find(&svs)
	switch cmd {
	case "/start", "/help":
		return "可用命令：\n/status 总览\n/vps 名称 VPS 详情\n/traffic 本月流量\n/routes 线路状态\n/sub 订阅链接"
	case "/status":
		online := 0
		var b strings.Builder
		for _, sv := range svs {
			if p.Hub.Online(sv.ID) {
				online++
			}
		}
		var firing int64
		p.St.DB.Model(&store.AlertEvent{}).Where("status = ?", "firing").Count(&firing)
		fmt.Fprintf(&b, "VPS 在线 %d/%d，告警中 %d\n", online, len(svs), firing)
		for _, sv := range svs {
			st := "🟢"
			if !p.Hub.Online(sv.ID) {
				st = "🔴"
			} else if sv.Blocked {
				st = "⛔"
			}
			line := fmt.Sprintf("%s %s", st, sv.Name)
			if m := p.Latest(sv.ID); m != nil && p.Hub.Online(sv.ID) {
				line += fmt.Sprintf("  CPU %.0f%%  ↓%s/s ↑%s/s", m.CPU, HumanBytes(m.NetIn), HumanBytes(m.NetOut))
			}
			if bh := p.backhaulAvg(sv.ID); bh > 0 {
				line += fmt.Sprintf("  回程 %.0fms", bh)
			}
			b.WriteString(line + "\n")
		}
		return b.String()
	case "/vps":
		if arg == "" {
			return "用法：/vps 名称"
		}
		for _, sv := range svs {
			if !strings.EqualFold(sv.Name, arg) && !strings.Contains(strings.ToLower(sv.Name), strings.ToLower(arg)) {
				continue
			}
			var b strings.Builder
			fmt.Fprintf(&b, "%s（%s）\nIP %s\n状态 %s\n", sv.Name, strings.Join(p.Roles(&sv), "/"), sv.IPv4, sv.Status)
			if m := p.Latest(sv.ID); m != nil {
				fmt.Fprintf(&b, "CPU %.1f%%  steal %.1f%%  负载 %.2f\n内存 %s  磁盘 %s\n连接 TCP %d / UDP %d\n", m.CPU, m.Steal, m.Load1, HumanBytes(m.MemUsed), HumanBytes(m.DiskUsed), m.TCPConns, m.UDPConns)
			}
			u := p.CycleUsage(&sv)
			fmt.Fprintf(&b, "本周期流量 %s", HumanBytes(u.Counted))
			if u.Quota > 0 {
				fmt.Fprintf(&b, " / %s（%.0f%%）", HumanBytes(uint64(u.Quota)), u.Percent)
			}
			if sv.ExpireAt != nil {
				fmt.Fprintf(&b, "\n到期 %s", sv.ExpireAt.Format("2006-01-02"))
			}
			if sv.Blocked {
				b.WriteString("\n⚠ 疑似 IP 被封")
			}
			return b.String()
		}
		return "未找到 " + arg
	case "/traffic":
		var b strings.Builder
		for _, sv := range svs {
			u := p.CycleUsage(&sv)
			line := fmt.Sprintf("%s  %s", sv.Name, HumanBytes(u.Counted))
			if u.Quota > 0 {
				line += fmt.Sprintf(" / %s（%.0f%%）", HumanBytes(uint64(u.Quota)), u.Percent)
			}
			b.WriteString(line + "\n")
		}
		if b.Len() == 0 {
			return "暂无 VPS"
		}
		return b.String()
	case "/routes":
		var b strings.Builder
		icons := map[string]string{"ok": "🟢", "degraded": "🟠", "down": "🔴", "disabled": "⚪"}
		for _, r := range p.RouteViews() {
			name := r.EntryName
			if r.Type == "chain" {
				name += " → " + r.LandingName
			}
			if r.Name != "" {
				name = r.Name + "（" + name + "）"
			}
			fmt.Fprintf(&b, "%s %s  %.0fms  今日 %s\n", icons[r.Status], name, r.LatencyMs, HumanBytes(r.TodayBytes))
		}
		if b.Len() == 0 {
			return "暂无线路"
		}
		return b.String()
	case "/sub":
		var b strings.Builder
		for _, t := range p.SubTokens() {
			if t.Enabled {
				fmt.Fprintf(&b, "%s：%s\n", t.Name, t.URL)
			}
		}
		if b.Len() == 0 {
			return "暂无订阅令牌"
		}
		return b.String()
	}
	return ""
}
