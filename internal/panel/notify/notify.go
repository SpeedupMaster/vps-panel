// Package notify delivers messages to Telegram, Bark, WeCom, Feishu, Email
// and generic webhooks.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	Title    string `json:"title"`
	Text     string `json:"text"`
	Severity string `json:"severity"` // info | warning | critical
	Kind     string `json:"kind"`
	Server   string `json:"server,omitempty"`
	TS       int64  `json:"ts"`
}

// Fields documents the config keys of each channel type (used by the UI).
var Fields = map[string][]string{
	"telegram": {"bot_token", "chat_id", "api_base", "bot_commands"},
	"bark":     {"server", "device_key"},
	"wecom":    {"webhook"},
	"feishu":   {"webhook", "secret"},
	"email":    {"host", "port", "username", "password", "from", "to"},
	"webhook":  {"url", "secret"},
}

var client = &http.Client{Timeout: 15 * time.Second}

func icon(sev string) string {
	switch sev {
	case "critical":
		return "🔴"
	case "warning":
		return "🟠"
	case "recovery":
		return "🟢"
	}
	return "🔵"
}

// Send delivers one message through one channel.
func Send(ctx context.Context, typ string, cfg map[string]string, m Message) error {
	switch typ {
	case "telegram":
		return Telegram(ctx, cfg, icon(m.Severity)+" "+m.Title+"\n"+m.Text)
	case "bark":
		server := strings.TrimRight(cfg["server"], "/")
		if server == "" {
			server = "https://api.day.app"
		}
		level := "active"
		if m.Severity == "critical" {
			level = "timeSensitive"
		}
		return postJSON(ctx, server+"/push", map[string]any{
			"device_key": cfg["device_key"], "title": m.Title, "body": m.Text, "group": "vps-panel", "level": level,
		}, nil)
	case "wecom":
		return postJSON(ctx, cfg["webhook"], map[string]any{"msgtype": "text", "text": map[string]string{"content": m.Title + "\n" + m.Text}}, nil)
	case "feishu":
		body := map[string]any{"msg_type": "text", "content": map[string]string{"text": m.Title + "\n" + m.Text}}
		if sec := cfg["secret"]; sec != "" {
			ts := strconv.FormatInt(time.Now().Unix(), 10)
			h := hmac.New(sha256.New, []byte(ts+"\n"+sec))
			body["timestamp"], body["sign"] = ts, base64.StdEncoding.EncodeToString(h.Sum(nil))
		}
		return postJSON(ctx, cfg["webhook"], body, nil)
	case "email":
		return Email(cfg, "[vps-panel] "+m.Title, m.Text)
	case "webhook":
		b, _ := json.Marshal(m)
		hdr := map[string]string{}
		if sec := cfg["secret"]; sec != "" {
			h := hmac.New(sha256.New, []byte(sec))
			h.Write(b)
			hdr["X-Signature"] = "sha256=" + hex.EncodeToString(h.Sum(nil))
		}
		return postRaw(ctx, cfg["url"], b, hdr)
	}
	return fmt.Errorf("未知渠道类型 %s", typ)
}

func postJSON(ctx context.Context, url string, v any, hdr map[string]string) error {
	b, _ := json.Marshal(v)
	return postRaw(ctx, url, b, hdr)
}

func postRaw(ctx context.Context, url string, body []byte, hdr map[string]string) error {
	if url == "" {
		return errors.New("缺少地址")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(rb)))
	}
	// WeCom / Feishu return 200 with an error code in the body.
	var r struct {
		ErrCode *int   `json:"errcode"`
		Code    *int   `json:"code"`
		Msg     string `json:"errmsg"`
		Msg2    string `json:"msg"`
	}
	if json.Unmarshal(rb, &r) == nil {
		if r.ErrCode != nil && *r.ErrCode != 0 {
			return fmt.Errorf("错误 %d: %s", *r.ErrCode, r.Msg)
		}
		if r.Code != nil && *r.Code != 0 && *r.Code != 200 {
			return fmt.Errorf("错误 %d: %s", *r.Code, r.Msg2)
		}
	}
	return nil
}

// ---------- Telegram ----------

func tgBase(cfg map[string]string) string {
	base := strings.TrimRight(cfg["api_base"], "/")
	if base == "" {
		base = "https://api.telegram.org"
	}
	return base + "/bot" + cfg["bot_token"]
}

func Telegram(ctx context.Context, cfg map[string]string, text string) error {
	return TelegramTo(ctx, cfg, cfg["chat_id"], text)
}

func TelegramTo(ctx context.Context, cfg map[string]string, chatID, text string) error {
	if cfg["bot_token"] == "" || chatID == "" {
		return errors.New("缺少 bot_token 或 chat_id")
	}
	if len(text) > 4000 {
		text = text[:4000] + "…"
	}
	return postJSON(ctx, tgBase(cfg)+"/sendMessage", map[string]any{"chat_id": chatID, "text": text, "disable_web_page_preview": true}, nil)
}

type TGUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
}

// TelegramUpdates long-polls getUpdates.
func TelegramUpdates(ctx context.Context, cfg map[string]string, offset int64) ([]TGUpdate, error) {
	url := fmt.Sprintf("%s/getUpdates?timeout=50&offset=%d&allowed_updates=%%5B%%22message%%22%%5D", tgBase(cfg), offset)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := (&http.Client{Timeout: 70 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool       `json:"ok"`
		Result      []TGUpdate `json:"result"`
		Description string     `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	if !r.OK {
		return nil, errors.New(r.Description)
	}
	return r.Result, nil
}

// SetTelegramCommands registers the bot menu.
func SetTelegramCommands(ctx context.Context, cfg map[string]string) error {
	cmds := []map[string]string{
		{"command": "status", "description": "总览"},
		{"command": "vps", "description": "VPS 详情：/vps 名称"},
		{"command": "traffic", "description": "本月流量"},
		{"command": "routes", "description": "线路状态"},
		{"command": "sub", "description": "订阅链接"},
	}
	return postJSON(ctx, tgBase(cfg)+"/setMyCommands", map[string]any{"commands": cmds}, nil)
}

// SendTelegramDocument uploads a file (used for backups).
func SendTelegramDocument(ctx context.Context, cfg map[string]string, name string, data []byte, caption string) error {
	var buf bytes.Buffer
	w := newMultipart(&buf)
	_ = w.WriteField("chat_id", cfg["chat_id"])
	_ = w.WriteField("caption", caption)
	fw, _ := w.CreateFormFile("document", name)
	_, _ = fw.Write(data)
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tgBase(cfg)+"/sendDocument", &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
	}
	return nil
}

// ---------- Email ----------

func Email(cfg map[string]string, subject, body string) error {
	host := cfg["host"]
	port := cfg["port"]
	if port == "" {
		port = "465"
	}
	if host == "" || cfg["to"] == "" {
		return errors.New("缺少 SMTP 服务器或收件人")
	}
	from := cfg["from"]
	if from == "" {
		from = cfg["username"]
	}
	to := strings.Split(cfg["to"], ",")
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
	}
	msg := "From: " + from + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: =?UTF-8?B?" +
		base64.StdEncoding.EncodeToString([]byte(subject)) + "?=\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		base64.StdEncoding.EncodeToString([]byte(body))
	addr := net.JoinHostPort(host, port)
	var auth smtp.Auth
	if cfg["username"] != "" {
		auth = smtp.PlainAuth("", cfg["username"], cfg["password"], host)
	}
	if port == "465" { // implicit TLS
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, &tls.Config{ServerName: host})
		if err != nil {
			return err
		}
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer c.Close()
		if auth != nil {
			if err := c.Auth(auth); err != nil {
				return err
			}
		}
		if err := c.Mail(from); err != nil {
			return err
		}
		for _, t := range to {
			if err := c.Rcpt(t); err != nil {
				return err
			}
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(msg)); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return c.Quit()
	}
	return smtp.SendMail(addr, auth, from, to, []byte(msg)) // STARTTLS when offered
}
