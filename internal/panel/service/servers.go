package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
)

type ServerInput struct {
	Name        string     `json:"name"`
	Group       string     `json:"group"`
	Tags        string     `json:"tags"`
	Mode        string     `json:"mode"`
	CapReality  bool       `json:"cap_reality"`
	CapLanding  bool       `json:"cap_landing"`
	Region      string     `json:"region"`
	CountryCode string     `json:"country_code"`
	Provider    string     `json:"provider"`
	ProviderURL string     `json:"provider_url"`
	Price       float64    `json:"price"`
	Currency    string     `json:"currency"`
	PayCycle    string     `json:"pay_cycle"`
	ExpireAt    *time.Time `json:"expire_at"`
	Note        string     `json:"note"`
	ResetDay    int        `json:"reset_day"`
	QuotaGB     float64    `json:"quota_gb"`
	CountMode   string     `json:"count_mode"`
	OnExceeded  string     `json:"on_exceeded"`
	Sort        int        `json:"sort"`
}

func (in *ServerInput) validate() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errors.New("名称不能为空")
	}
	if in.Mode != "probe" && in.Mode != "node" {
		return errors.New("模式只能是 probe 或 node")
	}
	if in.Mode == "probe" {
		in.CapReality, in.CapLanding = false, false
	} else if !in.CapReality && !in.CapLanding {
		return errors.New("节点机至少选择一种能力")
	}
	if in.ResetDay < 1 || in.ResetDay > 31 {
		in.ResetDay = 1
	}
	if in.CountMode == "" {
		in.CountMode = "out"
	}
	if in.OnExceeded == "" {
		in.OnExceeded = "alert"
	}
	if in.Currency == "" {
		in.Currency = "USD"
	}
	if in.PayCycle == "" {
		in.PayCycle = "month"
	}
	in.CountryCode = strings.ToUpper(in.CountryCode)
	return nil
}

func (in *ServerInput) apply(sv *store.Server) {
	sv.Name, sv.Group, sv.Tags, sv.Sort = in.Name, in.Group, in.Tags, in.Sort
	sv.Region, sv.CountryCode = in.Region, in.CountryCode
	sv.Provider, sv.ProviderURL, sv.Price, sv.Currency, sv.PayCycle, sv.ExpireAt, sv.Note =
		in.Provider, in.ProviderURL, in.Price, in.Currency, in.PayCycle, in.ExpireAt, in.Note
	sv.ResetDay, sv.QuotaBytes, sv.CountMode, sv.OnExceeded = in.ResetDay, int64(in.QuotaGB*(1<<30)), in.CountMode, in.OnExceeded
}

// CreateServer adds a server and returns its one-time install token.
func (p *Panel) CreateServer(in ServerInput) (*store.Server, string, error) {
	if err := in.validate(); err != nil {
		return nil, "", err
	}
	sv := &store.Server{Mode: in.Mode, CapReality: in.CapReality, CapLanding: in.CapLanding, Status: "pending", AgentID: "pending-" + secret.RandomToken(8)}
	in.apply(sv)
	token := secret.RandomToken(24)
	sv.InstallTokenHash = secret.Hash(token)
	sv.InstallTokenExp = time.Now().Add(24 * time.Hour)
	if err := p.St.DB.Create(sv).Error; err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, "", errors.New("名称已存在")
		}
		return nil, "", err
	}
	return sv, token, nil
}

// UpdateServer edits asset / billing info. Mode and capabilities change
// through SetMode because they install or remove Xray.
func (p *Panel) UpdateServer(id uint, in ServerInput) error {
	var sv store.Server
	if err := p.St.DB.First(&sv, id).Error; err != nil {
		return err
	}
	in.Mode = sv.Mode
	in.CapReality, in.CapLanding = sv.CapReality, sv.CapLanding
	if err := in.validate(); err != nil {
		return err
	}
	in.apply(&sv)
	return p.St.DB.Save(&sv).Error
}

// NewInstallToken issues a fresh token (e.g. to reinstall an agent).
func (p *Panel) NewInstallToken(id uint) (string, error) {
	token := secret.RandomToken(24)
	err := p.St.DB.Model(&store.Server{}).Where("id = ?", id).Updates(map[string]any{
		"install_token_hash": secret.Hash(token), "install_token_exp": time.Now().Add(24 * time.Hour),
	}).Error
	return token, err
}

func (p *Panel) InstallCommand(token, mode string) string {
	base := strings.TrimRight(p.St.Get("panel_url", "https://panel.example.com"), "/")
	return fmt.Sprintf("bash <(curl -fsSL %s/install.sh) --panel %s --token %s --mode %s", base, base, token, mode)
}

func (p *Panel) DeleteServer(id uint) error {
	var n int64
	p.St.DB.Model(&store.Route{}).Where("entry_server_id = ? OR landing_server_id = ?", id, id).Count(&n)
	if n > 0 {
		return errors.New("还有线路使用这台服务器，请先删除相关线路")
	}
	db := p.St.DB
	for _, t := range []string{"metrics_raw", "metrics_1m", "metrics_1h", "metrics_1d", "probe_results", "probe_results_1h"} {
		db.Exec("DELETE FROM "+t+" WHERE server_id = ?", id)
	}
	db.Delete(&store.RealityInbound{}, "server_id = ?", id)
	db.Delete(&store.LandingInbound{}, "server_id = ?", id)
	db.Delete(&store.LandingUser{}, "landing_server_id = ? OR entry_server_id = ?", id, id)
	db.Delete(&store.ConfigVersion{}, "server_id = ?", id)
	return db.Delete(&store.Server{}, id).Error
}

// SystemInfo decodes the last Hello's system info.
func SystemInfo(sv *store.Server) proto.SystemInfo {
	var s proto.SystemInfo
	_ = json.Unmarshal([]byte(sv.SystemJSON), &s)
	return s
}

func Switches(sv *store.Server) proto.LocalSwitches {
	var s proto.LocalSwitches
	_ = json.Unmarshal([]byte(sv.SwitchesJSON), &s)
	return s
}

// CycleStart returns the start of the current billing cycle.
func CycleStart(resetDay int, now time.Time) time.Time {
	if resetDay < 1 {
		resetDay = 1
	}
	y, m, _ := now.Date()
	start := clampDay(y, m, resetDay, now.Location())
	if now.Before(start) {
		start = clampDay(y, m-1, resetDay, now.Location())
	}
	return start
}

func clampDay(y int, m time.Month, d int, loc *time.Location) time.Time {
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, loc).Day()
	if d > last {
		d = last
	}
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// MonthlyCost normalizes a price to per-month.
func MonthlyCost(sv *store.Server) float64 {
	switch sv.PayCycle {
	case "quarter":
		return sv.Price / 3
	case "half":
		return sv.Price / 6
	case "year":
		return sv.Price / 12
	case "once":
		return 0
	}
	return sv.Price
}
