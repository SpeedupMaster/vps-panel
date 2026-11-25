package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/notify"
	"vpspanel/internal/panel/store"
)

// ---------- alert rules, events & channel queries ----------

func (p *Panel) ListAlertRules() []store.AlertRule {
	var rs []store.AlertRule
	p.St.R().Order("id").Find(&rs)
	return rs
}

func (p *Panel) SaveAlertRule(r *store.AlertRule, ip string) error {
	if r.Name == "" || KindLabels[r.Kind] == "" {
		return errors.New("名称或类型无效")
	}
	if r.Severity == "" {
		r.Severity = "warning"
	}
	if r.Scope == "" {
		r.Scope = "all"
	}
	if err := p.St.DB.Save(r).Error; err != nil {
		return err
	}
	p.Audit("alert", "save_rule", r.Name, "", ip)
	return nil
}

// DeleteAlertRule resolves any active firing AlertEvent rows for rule-<id>,
// clears AlertEngine.since duration trackers, deletes the rule, and audits.
func (p *Panel) DeleteAlertRule(id uint, ip string) error {
	var rule store.AlertRule
	target := strconv.FormatUint(uint64(id), 10)
	if p.St.DB.First(&rule, id).Error == nil && rule.Name != "" {
		target = rule.Name
	}
	key := fmt.Sprintf("rule-%d", id)
	now := time.Now()
	var resolvedIDs []uint
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		var firing []store.AlertEvent
		if err := tx.Select("id").Where("key = ? AND status = ?", key, "firing").Find(&firing).Error; err != nil {
			return err
		}
		for _, ev := range firing {
			resolvedIDs = append(resolvedIDs, ev.ID)
		}
		if len(resolvedIDs) > 0 {
			if err := tx.Model(&store.AlertEvent{}).Where("id IN ?", resolvedIDs).
				Updates(map[string]any{"status": "resolved", "resolved_at": &now}).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&store.AlertRule{}, id).Error
	}); err != nil {
		return err
	}
	if eng, ok := p.Alert.(*AlertEngine); ok {
		eng.ForgetRule(id)
	}
	for _, evID := range resolvedIDs {
		p.Bus.Publish("alert", map[string]any{"id": evID, "status": "resolved"})
	}
	p.Audit("alert", "delete_rule", target, strconv.FormatUint(uint64(id), 10), ip)
	return nil
}

type AlertEventFilter struct {
	Status   string
	Kind     string
	ServerID string
	Severity string
	Limit    int
}

func (p *Panel) ListAlertEvents(f AlertEventFilter) ([]store.AlertEvent, int64) {
	q := p.St.R().Order("id desc")
	if f.Status != "" && f.Status != "all" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	if f.ServerID != "" {
		q = q.Where("server_id = ?", f.ServerID)
	}
	if f.Severity != "" {
		q = q.Where("severity = ?", f.Severity)
	}
	limit := f.Limit
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	var evs []store.AlertEvent
	q.Limit(limit).Find(&evs)
	var firing int64
	p.St.R().Model(&store.AlertEvent{}).Where("status = ? AND acked = ?", "firing", false).Count(&firing)
	return evs, firing
}

func (p *Panel) AckAlertEvent(id uint) error {
	if err := p.St.DB.Model(&store.AlertEvent{}).Where("id = ?", id).Update("acked", true).Error; err != nil {
		return err
	}
	p.Bus.Publish("alert", map[string]any{"id": id, "acked": true})
	return nil
}

func (p *Panel) AckAllAlertEvents() error {
	return p.St.DB.Model(&store.AlertEvent{}).
		Where("status IN ? AND acked = ?", []string{"firing", "info"}, false).
		Update("acked", true).Error
}

func (p *Panel) SilenceAlertEvent(id uint, minutes int) (time.Time, error) {
	if minutes <= 0 {
		minutes = 60
	}
	to := time.Now().Add(time.Duration(minutes) * time.Minute)
	if err := p.St.DB.Model(&store.AlertEvent{}).Where("id = ?", id).Update("silenced_to", &to).Error; err != nil {
		return time.Time{}, err
	}
	return to, nil
}

func (p *Panel) AlertOverview(limit int) (int64, []store.AlertEvent) {
	if limit <= 0 {
		limit = 6
	}
	db := p.St.R()
	var firing int64
	db.Model(&store.AlertEvent{}).Where("status = ?", "firing").Count(&firing)
	var recent []store.AlertEvent
	db.Order("started_at desc").Limit(limit).Find(&recent)
	return firing, recent
}

func (p *Panel) ListNotifyChannels() []store.NotifyChannel {
	var chs []store.NotifyChannel
	p.St.R().Order("id").Find(&chs)
	return chs
}

func (p *Panel) DeleteNotifyChannel(id uint) error {
	return p.St.DB.Delete(&store.NotifyChannel{}, id).Error
}

// ChannelConfig decrypts a channel's settings.
func (p *Panel) ChannelConfig(ch *store.NotifyChannel) (map[string]string, error) {
	s, err := p.Box.Open(ch.ConfigEnc)
	if err != nil {
		return nil, err
	}
	cfg := map[string]string{}
	return cfg, json.Unmarshal([]byte(s), &cfg)
}

func (p *Panel) SaveChannel(ch *store.NotifyChannel, cfg map[string]string) error {
	if _, ok := notify.Fields[ch.Type]; !ok {
		return fmt.Errorf("未知渠道类型 %s", ch.Type)
	}
	if ch.ID != 0 { // keep secrets the UI didn't resend
		var old store.NotifyChannel
		if p.St.DB.First(&old, ch.ID).Error == nil {
			if prev, err := p.ChannelConfig(&old); err == nil {
				for k, v := range prev {
					if cfg[k] == "" || cfg[k] == "••••••" {
						cfg[k] = v
					}
				}
			}
		}
	}
	b, _ := json.Marshal(cfg)
	ch.ConfigEnc = p.Box.Seal(string(b))
	if ch.MinLevel == "" {
		ch.MinLevel = "info"
	}
	return p.St.DB.Save(ch).Error
}

// TestChannel sends a test message synchronously.
func (p *Panel) TestChannel(id uint) error {
	var ch store.NotifyChannel
	if err := p.St.DB.First(&ch, id).Error; err != nil {
		return err
	}
	cfg, err := p.ChannelConfig(&ch)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return notify.Send(ctx, ch.Type, cfg, notify.Message{Title: "测试通知", Text: "来自 vps-panel 的测试消息，收到说明渠道配置正确。", Severity: "info", Kind: "test", TS: time.Now().Unix()})
}

// checkResourceAlerts feeds metric frames into threshold rules.
func (p *Panel) checkResourceAlerts(serverID uint, m *proto.Metrics) {
	p.Alert.Observe("cpu", serverID, m.CPU)
	p.Alert.Observe("steal", serverID, m.Steal)
	var sys proto.SystemInfo
	if c := p.Hub.Conn(serverID); c != nil && c.Hello() != nil {
		sys = c.Hello().System
	}
	if sys.MemTotal > 0 {
		p.Alert.Observe("mem", serverID, float64(m.MemUsed)/float64(sys.MemTotal)*100)
	}
	if sys.DiskTotal > 0 {
		p.Alert.Observe("disk", serverID, float64(m.DiskUsed)/float64(sys.DiskTotal)*100)
	}
	if sys.CPUCores > 0 {
		p.Alert.Observe("load", serverID, m.Load1/float64(sys.CPUCores))
	}
}
