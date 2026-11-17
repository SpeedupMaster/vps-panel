package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/xrayconf"
	"vpspanel/internal/panel/store"
)

type RouteInput struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	EntryServerID   uint   `json:"entry_server_id"`
	LandingServerID uint   `json:"landing_server_id"`
	BackupIDs       []uint `json:"backup_ids"`
	Enabled         *bool  `json:"enabled"`
	InSub           *bool  `json:"in_sub"`
	Sort            int    `json:"sort"`
	Tags            string `json:"tags"`
}

func joinIDs(ids []uint) string {
	s := make([]string, len(ids))
	for i, id := range ids {
		s[i] = strconv.FormatUint(uint64(id), 10)
	}
	return strings.Join(s, ",")
}

func (p *Panel) validateRoute(in *RouteInput) error {
	in.Name = strings.TrimSpace(in.Name)
	var entry store.Server
	if err := p.St.DB.First(&entry, in.EntryServerID).Error; err != nil {
		return errors.New("入口机不存在")
	}
	if entry.Mode != "node" || !entry.CapReality {
		return errors.New(entry.Name + " 没有开启 Reality 入站能力")
	}
	switch in.Type {
	case "direct":
		in.LandingServerID, in.BackupIDs = 0, nil
		if in.Name == "" {
			in.Name = entry.Name + " 直连"
		}
	case "chain":
		var landing store.Server
		if err := p.St.DB.First(&landing, in.LandingServerID).Error; err != nil {
			return errors.New("落地机不存在")
		}
		if landing.Mode != "node" || !landing.CapLanding {
			return errors.New(landing.Name + " 没有开启落地能力")
		}
		if landing.ID == entry.ID {
			return errors.New("入口和落地不能是同一台")
		}
		seen := map[uint]bool{landing.ID: true}
		var bs []uint
		for _, b := range in.BackupIDs {
			if seen[b] || b == entry.ID {
				continue
			}
			var bsv store.Server
			if err := p.St.DB.First(&bsv, b).Error; err != nil || !bsv.CapLanding {
				return fmt.Errorf("备用落地 #%d 无效", b)
			}
			seen[b] = true
			bs = append(bs, b)
		}
		in.BackupIDs = bs
		if in.Name == "" {
			in.Name = entry.Name + " → " + landing.Name
		}
	default:
		return errors.New("类型只能是 direct 或 chain")
	}
	return nil
}

// CreateRoute adds a route and hot-adds its user on the entry machine.
func (p *Panel) CreateRoute(in RouteInput) (*store.Route, error) {
	if err := p.validateRoute(&in); err != nil {
		return nil, err
	}
	r := &store.Route{
		Name: in.Name, Type: in.Type, EntryServerID: in.EntryServerID, LandingServerID: in.LandingServerID,
		BackupIDs: joinIDs(in.BackupIDs), UUID: NewUUID(), Email: "tmp-" + NewShortID(), Enabled: true, InSub: true,
		Sort: in.Sort, Tags: in.Tags, CreatedAt: time.Now(),
	}
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	if in.InSub != nil {
		r.InSub = *in.InSub
	}
	if err := p.St.DB.Create(r).Error; err != nil {
		return nil, err
	}
	r.Email = fmt.Sprintf("r-%d", r.ID)
	p.St.DB.Model(r).Update("email", r.Email)
	return r, p.syncRoute(r, nil, "新增线路 "+r.Name)
}

func (p *Panel) UpdateRoute(id uint, in RouteInput) (*store.Route, error) {
	var r store.Route
	if err := p.St.DB.First(&r, id).Error; err != nil {
		return nil, err
	}
	old := r
	if err := p.validateRoute(&in); err != nil {
		return nil, err
	}
	r.Name, r.Type, r.EntryServerID, r.LandingServerID, r.BackupIDs, r.Sort, r.Tags =
		in.Name, in.Type, in.EntryServerID, in.LandingServerID, joinIDs(in.BackupIDs), in.Sort, in.Tags
	if in.Enabled != nil {
		r.Enabled = *in.Enabled
	}
	if in.InSub != nil {
		r.InSub = *in.InSub
	}
	if err := p.St.DB.Save(&r).Error; err != nil {
		return nil, err
	}
	return &r, p.syncRoute(&r, &old, "修改线路 "+r.Name)
}

func (p *Panel) DeleteRoute(id uint) error {
	var r store.Route
	if err := p.St.DB.First(&r, id).Error; err != nil {
		return err
	}
	if err := p.St.DB.Delete(&r).Error; err != nil {
		return err
	}
	old := r
	r.Enabled = false
	return p.syncRoute(&r, &old, "删除线路 "+r.Name)
}

// SetRouteEnabled toggles a route (hot remove / add).
func (p *Panel) SetRouteEnabled(id uint, on bool) error {
	var r store.Route
	if err := p.St.DB.First(&r, id).Error; err != nil {
		return err
	}
	old := r
	r.Enabled = on
	p.St.DB.Model(&r).Update("enabled", on)
	return p.syncRoute(&r, &old, map[bool]string{true: "启用", false: "停用"}[on]+"线路 "+r.Name)
}

// RegenRouteUUID gives a single route a new UUID (e.g. one leaked link).
func (p *Panel) RegenRouteUUID(id uint) error {
	var r store.Route
	if err := p.St.DB.First(&r, id).Error; err != nil {
		return err
	}
	old := r
	r.UUID = NewUUID()
	p.St.DB.Model(&r).Update("uuid", r.UUID)
	_, err := p.Deploy(r.EntryServerID, "重置线路 UUID "+old.Name, nil)
	return err
}

func landingsOf(r *store.Route) []uint {
	if r == nil || r.Type != "chain" || !r.Enabled {
		return nil
	}
	return append([]uint{r.LandingServerID}, parseIDs(r.BackupIDs)...)
}

// syncRoute pushes the minimal set of changes: landings whose user set
// changed get redeployed, and the entry is hot-updated when only one user
// was added or removed and the outbound already existed.
func (p *Panel) syncRoute(r, old *store.Route, reason string) error {
	// Landings first: the entry must not dial a landing that doesn't know it yet.
	touched := map[uint]bool{}
	for _, l := range append(landingsOf(r), landingsOf(old)...) {
		touched[l] = true
	}
	for l := range touched {
		_, created, err := p.landingUser(l, r.EntryServerID)
		if err != nil {
			return err
		}
		var op *proto.XrayOp
		if created {
			u, _, _ := p.landingUser(l, r.EntryServerID)
			key, _ := p.Box.Open(u.UserKeyEnc)
			op = &proto.XrayOp{Op: "add_user", Tag: "ss-in", User: &proto.XrayUser{Email: u.Email, Password: key}}
		}
		if _, err := p.Deploy(l, reason, op); err != nil {
			return err
		}
	}
	entries := map[uint]bool{r.EntryServerID: true}
	if old != nil {
		entries[old.EntryServerID] = true
	}
	for e := range entries {
		var op *proto.XrayOp
		if e == r.EntryServerID {
			op = p.hotOp(r, old)
		}
		if _, err := p.Deploy(e, reason, op); err != nil {
			return err
		}
	}
	p.Audit("route", "sync", r.Name, reason, "")
	p.Bus.Publish("route", map[string]any{"id": r.ID})
	return nil
}

// hotOp returns an add/remove op when the change is a single user whose
// destination already exists in the running config; nil means full apply.
func (p *Panel) hotOp(r, old *store.Route) *proto.XrayOp {
	wasOn := old != nil && old.Enabled && old.EntryServerID == r.EntryServerID
	nowOn := r.Enabled
	sameDest := old != nil && old.Type == r.Type && old.LandingServerID == r.LandingServerID && old.BackupIDs == r.BackupIDs && old.UUID == r.UUID
	switch {
	case !wasOn && nowOn:
		dest, ok := p.existingDest(r)
		if !ok {
			return nil
		}
		rule, _ := json.Marshal(xrayconf.UserRule(dest))
		return &proto.XrayOp{Op: "add_user", Tag: "reality-in", User: &proto.XrayUser{Email: r.Email, ID: r.UUID, Flow: "xtls-rprx-vision"}, Rule: rule}
	case wasOn && !nowOn:
		return &proto.XrayOp{Op: "remove_user", Tag: "reality-in", User: &proto.XrayUser{Email: r.Email}, Rule: json.RawMessage(`{}`)}
	case wasOn && nowOn && sameDest:
		return nil // name / sort / tags only: config unchanged
	}
	return nil
}

// existingDest checks whether the entry's applied config already has the
// outbound (no balancer needed) for this route.
func (p *Panel) existingDest(r *store.Route) (xrayconf.User, bool) {
	u := xrayconf.User{Email: r.Email, UUID: r.UUID, Outbound: "direct"}
	if r.Type == "direct" {
		return u, true
	}
	if r.BackupIDs != "" {
		return u, false // balancer: new routing object, needs a restart
	}
	last := p.latestVersion(r.EntryServerID)
	if last == nil || !last.Applied {
		return u, false
	}
	var cfg struct {
		Outbounds []struct{ Tag string } `json:"outbounds"`
	}
	_ = json.Unmarshal([]byte(last.Config), &cfg)
	for _, o := range cfg.Outbounds {
		if strings.HasPrefix(o.Tag, outTag(r.LandingServerID)) {
			u.Outbound = o.Tag
			return u, true
		}
	}
	return u, false
}

// RouteView adds display data.
type RouteView struct {
	store.Route
	EntryName    string   `json:"entry_name"`
	LandingName  string   `json:"landing_name"`
	BackupNames  []string `json:"backup_names"`
	EntryCC      string   `json:"entry_cc"`
	LandingCC    string   `json:"landing_cc"`
	Status       string   `json:"status"` // ok | degraded | down | disabled
	LatencyMs    float64  `json:"latency_ms"`
	BackhaulMs   float64  `json:"backhaul_ms"`
	SegmentMs    float64  `json:"segment_ms"`
	TodayBytes   uint64   `json:"today_bytes"`
	MonthBytes   uint64   `json:"month_bytes"`
	ShareLink    string   `json:"share_link"`
	XrayRunning  bool     `json:"xray_running"`
	EntryBlocked bool     `json:"entry_blocked"`
	// ConfigSynced: every machine of the route runs the latest config
	// version without drift. DeployError: why the last push failed, if it did.
	ConfigSynced bool   `json:"config_synced"`
	DeployError  string `json:"deploy_error,omitempty"`
}

func (p *Panel) RouteViews() []RouteView {
	var rs []store.Route
	p.St.DB.Order("sort, id").Find(&rs)
	var svs []store.Server
	p.St.DB.Find(&svs)
	byID := map[uint]*store.Server{}
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	latest := map[uint]int{}
	var vers []struct {
		ServerID uint
		V        int
	}
	p.St.DB.Model(&store.ConfigVersion{}).Select("server_id, MAX(version) AS v").Group("server_id").Scan(&vers)
	for _, x := range vers {
		latest[x.ServerID] = x.V
	}
	synced := func(sv *store.Server) bool {
		return sv != nil && latest[sv.ID] > 0 && sv.XrayConfigVer >= latest[sv.ID] && !sv.XrayDrift
	}
	out := make([]RouteView, 0, len(rs))
	for _, r := range rs {
		v := RouteView{Route: r, Status: "ok"}
		e := byID[r.EntryServerID]
		if e != nil {
			v.EntryName, v.EntryCC, v.XrayRunning, v.EntryBlocked = e.Name, e.CountryCode, e.XrayRunning, e.Blocked
		}
		if l := byID[r.LandingServerID]; l != nil {
			v.LandingName, v.LandingCC = l.Name, l.CountryCode
		}
		for _, b := range parseIDs(r.BackupIDs) {
			if s := byID[b]; s != nil {
				v.BackupNames = append(v.BackupNames, s.Name)
			}
		}
		id := strconv.FormatUint(uint64(r.ID), 10)
		up, down := p.Sum("route", id, day)
		v.TodayBytes = up + down
		up, down = p.Sum("route", id, month)
		v.MonthBytes = up + down
		v.BackhaulMs = p.backhaulAvg(r.EntryServerID)
		v.LatencyMs = v.BackhaulMs
		if r.Type == "chain" {
			v.SegmentMs = p.segmentAvg(r.EntryServerID, r.LandingServerID)
			v.LatencyMs += v.SegmentMs
		}
		switch {
		case !r.Enabled:
			v.Status = "disabled"
		case e == nil || e.Status == "offline" || !e.XrayRunning || e.Blocked:
			v.Status = "down"
		case r.Type == "chain" && p.segmentDown(r.EntryServerID, r.LandingServerID):
			if len(parseIDs(r.BackupIDs)) > 0 {
				v.Status = "degraded"
			} else {
				v.Status = "down"
			}
		case r.Type == "chain" && byID[r.LandingServerID] != nil && byID[r.LandingServerID].Status == "offline":
			v.Status = "down"
		}
		v.ConfigSynced = synced(e) && (r.Type != "chain" || synced(byID[r.LandingServerID]))
		if !v.ConfigSynced {
			for _, sid := range []uint{r.EntryServerID, r.LandingServerID} {
				if msg := p.lastDeployError(sid); sid != 0 && msg != "" {
					v.DeployError = msg
					break
				}
			}
		}
		if e != nil {
			var ri store.RealityInbound
			if p.St.DB.First(&ri, "server_id = ?", e.ID).Error == nil {
				v.ShareLink = VLESSLink(nodeFor(&r, e, byID[r.LandingServerID], &ri, ""))
			}
		}
		out = append(out, v)
	}
	return out
}

// lastDeployError is the last line of the most recent config push to the
// server when that push failed, else "".
func (p *Panel) lastDeployError(serverID uint) string {
	var t store.Task
	if p.St.DB.Where("server_id = ? AND type IN ? AND finished_at IS NOT NULL", serverID, []string{"apply_config", "xray_op"}).
		Order("id DESC").First(&t).Error != nil || t.Status == "success" {
		return ""
	}
	return lastLine(t.Output)
}

// ---------- topology ----------

type TopoNode struct {
	ID          uint     `json:"id"`
	Name        string   `json:"name"`
	CountryCode string   `json:"country_code"`
	Roles       []string `json:"roles"`
	Status      string   `json:"status"`
	Blocked     bool     `json:"blocked"`
	BackhaulMs  float64  `json:"backhaul_ms"`
}

type TopoEdge struct {
	From       uint     `json:"from"`
	To         uint     `json:"to"` // 0 = internet (direct)
	Routes     []uint   `json:"routes"`
	Names      []string `json:"names"`
	Backup     bool     `json:"backup"`
	SegmentMs  float64  `json:"segment_ms"`
	Down       bool     `json:"down"`
	TodayBytes uint64   `json:"today_bytes"`
}

func (p *Panel) Topology() map[string]any {
	var svs []store.Server
	p.St.DB.Where("mode = ?", "node").Order("sort, id").Find(&svs)
	nodes := make([]TopoNode, 0, len(svs))
	for i := range svs {
		sv := &svs[i]
		nodes = append(nodes, TopoNode{sv.ID, sv.Name, sv.CountryCode, p.Roles(sv), sv.Status, sv.Blocked, p.backhaulAvg(sv.ID)})
	}
	edges := map[[2]uint]*TopoEdge{}
	get := func(from, to uint, backup bool) *TopoEdge {
		k := [2]uint{from, to}
		if e := edges[k]; e != nil {
			e.Backup = e.Backup && backup
			return e
		}
		e := &TopoEdge{From: from, To: to, Backup: backup}
		if to != 0 {
			e.SegmentMs = p.segmentAvg(from, to)
			e.Down = p.segmentDown(from, to)
		}
		edges[k] = e
		return e
	}
	for _, v := range p.RouteViews() {
		if !v.Enabled {
			continue
		}
		if v.Type == "direct" {
			e := get(v.EntryServerID, 0, false)
			e.Routes, e.Names = append(e.Routes, v.ID), append(e.Names, v.Name)
			e.TodayBytes += v.TodayBytes
			continue
		}
		e := get(v.EntryServerID, v.LandingServerID, false)
		e.Routes, e.Names = append(e.Routes, v.ID), append(e.Names, v.Name)
		e.TodayBytes += v.TodayBytes
		for _, b := range parseIDs(v.BackupIDs) {
			be := get(v.EntryServerID, b, true)
			be.Routes = append(be.Routes, v.ID)
		}
	}
	list := make([]*TopoEdge, 0, len(edges))
	for _, e := range edges {
		list = append(list, e)
	}
	return map[string]any{"nodes": nodes, "edges": list}
}
