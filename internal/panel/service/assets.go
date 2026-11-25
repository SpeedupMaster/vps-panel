package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

// Roles derives display roles from mode, capabilities and routes.
func (p *Panel) Roles(sv *store.Server) []string {
	if sv.Mode != "node" {
		return []string{"probe"}
	}
	var roles []string
	if sv.CapReality {
		var direct, chain int64
		p.St.DB.Model(&store.Route{}).Where("entry_server_id = ? AND type = ?", sv.ID, "direct").Count(&direct)
		p.St.DB.Model(&store.Route{}).Where("entry_server_id = ? AND type = ?", sv.ID, "chain").Count(&chain)
		if chain > 0 {
			roles = append(roles, "entry")
		}
		if direct > 0 || chain == 0 {
			roles = append(roles, "direct")
		}
	}
	if sv.CapLanding {
		roles = append(roles, "landing")
	}
	return roles
}

// ToCurrency converts an amount to the display currency using the fx_rates
// setting (JSON map of currency -> units per 1 display currency).
func (p *Panel) ToCurrency(amount float64, cur string) float64 {
	base := p.St.Get("currency", "USD")
	if cur == "" || strings.EqualFold(cur, base) {
		return amount
	}
	// Saved rates are merged over the defaults, so currencies added later still
	// convert for panels that saved fx_rates before they existed.
	rates := map[string]float64{"CNY": 7.2, "EUR": 0.92, "HKD": 7.8, "JPY": 150, "GBP": 0.79, "CAD": 1.38}
	_ = json.Unmarshal([]byte(p.St.Get("fx_rates", "{}")), &rates)
	if r, ok := rates[strings.ToUpper(cur)]; ok && r > 0 {
		return amount / r
	}
	return amount
}

// OnSettingsChanged pushes changed agent-facing settings to online agents.
func (p *Panel) OnSettingsChanged(keys []string) {
	push := false
	probes := false
	for _, k := range keys {
		switch k {
		case "agent_report_interval", "panel_url", "panel_urls_backup":
			push = true
		case "probe_interval":
			probes = true
		case "landing_firewall":
			go p.RedeployLandings("落地防火墙设置变更")
		}
	}
	for _, id := range p.Hub.OnlineIDs() {
		if push {
			_ = p.Hub.Send(id, &proto.PanelMessage{Settings: p.agentSettings()})
		}
		if probes {
			p.PushProbeTasks(id)
		}
	}
}

func (p *Panel) agentSettings() *proto.AgentSettings {
	urls := []string{strings.TrimRight(p.St.Get("panel_url", ""), "/")}
	for _, u := range strings.Split(p.St.Get("panel_urls_backup", ""), "\n") {
		if u = strings.TrimSpace(u); u != "" {
			urls = append(urls, strings.TrimRight(u, "/"))
		}
	}
	return &proto.AgentSettings{ReportInterval: p.St.GetInt("agent_report_interval", 10), PanelURLs: urls}
}

// CheckExpiry fires reminders 7 days and 1 day before a VPS expires, and
// auto-resolves firing expiry alerts once a server is renewed or cleared.
func (p *Panel) CheckExpiry() {
	var svs []store.Server
	p.St.DB.Find(&svs)
	byID := make(map[uint]*store.Server, len(svs))
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}

	var active []store.AlertEvent
	p.St.DB.Where("kind = ? AND status = ?", "expire", "firing").Find(&active)
	for _, ev := range active {
		sv := byID[ev.ServerID]
		if sv == nil || sv.ExpireAt == nil {
			p.Alert.Resolve("expire", ev.ServerID, ev.Key)
			continue
		}
		days := int(time.Until(*sv.ExpireAt).Hours()/24 + 0.999)
		dateSuffix := "-" + sv.ExpireAt.Format("20060102")
		if days > 7 || !strings.HasSuffix(ev.Key, dateSuffix) {
			p.Alert.Resolve("expire", ev.ServerID, ev.Key)
		}
	}

	for _, sv := range svs {
		if sv.ExpireAt == nil {
			continue
		}
		days := int(time.Until(*sv.ExpireAt).Hours()/24 + 0.999)
		for _, d := range []int{1, 7} {
			if days <= d && days >= 0 {
				key := fmt.Sprintf("expire-%d-%s", d, sv.ExpireAt.Format("20060102"))
				p.Alert.Fire("expire", sv.ID, key, "info", fmt.Sprintf("%s %d 天后到期（%s）", sv.Name, days, sv.ExpireAt.Format("2006-01-02")))
				break
			}
		}
	}
}
