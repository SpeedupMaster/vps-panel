package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

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
	if err := p.St.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(r).Error; err != nil {
			return err
		}
		r.Email = fmt.Sprintf("r-%d", r.ID)
		return tx.Model(r).Update("email", r.Email).Error
	}); err != nil {
		return nil, err
	}
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
	if err := p.St.DB.Model(&r).Update("enabled", on).Error; err != nil {
		return err
	}
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
	if err := p.St.DB.Model(&r).Update("uuid", r.UUID).Error; err != nil {
		return err
	}
	_, err := p.Deploy(r.EntryServerID, "重置线路 UUID "+old.Name, nil)
	return err
}

// ServerRoutes returns all routes whose entry is serverID.
func (p *Panel) ServerRoutes(serverID uint) []store.Route {
	var routes []store.Route
	p.St.R().Where("entry_server_id = ?", serverID).Order("sort, id").Find(&routes)
	return routes
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
	// The entry's segment probe targets follow its routes: refresh them even
	// if a deploy below fails, since the route itself is already saved.
	defer func() {
		p.PushProbeTasks(r.EntryServerID)
		if old != nil && old.EntryServerID != r.EntryServerID {
			p.PushProbeTasks(old.EntryServerID)
		}
	}()
	// Landings first: the entry must not dial a landing that doesn't know it yet.
	activeLandings := map[uint]bool{}
	for _, l := range landingsOf(r) {
		activeLandings[l] = true
	}
	touched := map[uint]bool{}
	for _, l := range append(landingsOf(r), landingsOf(old)...) {
		touched[l] = true
	}
	for l := range touched {
		var op *proto.XrayOp
		if activeLandings[l] {
			u, created, err := p.landingUser(l, r.EntryServerID)
			if err != nil {
				return err
			}
			if created {
				key, _ := p.Box.Open(u.UserKeyEnc)
				op = &proto.XrayOp{Op: "add_user", Tag: "ss-in", User: &proto.XrayUser{Email: u.Email, Password: key}}
			}
		}
		p.pruneOrphanLandingUsers(l)
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
	SegmentDown  bool     `json:"segment_down"` // last segment probe lost every packet
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
	db := p.St.R()
	var rs []store.Route
	db.Order("sort, id").Find(&rs)
	var svs []store.Server
	db.Find(&svs)
	byID := map[uint]*store.Server{}
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}
	var ris []store.RealityInbound
	db.Find(&ris)
	riBy := make(map[uint]*store.RealityInbound, len(ris))
	for i := range ris {
		riBy[ris[i].ServerID] = &ris[i]
	}
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	dayHour := day.Unix() / 3600 * 3600
	monthHour := month.Unix() / 3600 * 3600

	todayBytes := map[string]uint64{}
	monthBytes := map[string]uint64{}
	if len(rs) > 0 {
		var trows []store.TrafficHourly
		db.Select("scope_id, hour, up, down").Where("scope = ? AND hour >= ?", "route", monthHour).Find(&trows)
		for _, tr := range trows {
			sum := tr.Up + tr.Down
			monthBytes[tr.ScopeID] += sum
			if tr.Hour >= dayHour {
				todayBytes[tr.ScopeID] += sum
			}
		}
	}

	latest := map[uint]int{}
	var vers []struct {
		ServerID uint
		V        int
	}
	db.Model(&store.ConfigVersion{}).Select("server_id, MAX(version) AS v").Group("server_id").Scan(&vers)
	for _, x := range vers {
		latest[x.ServerID] = x.V
	}
	synced := func(sv *store.Server) bool {
		return sv != nil && latest[sv.ID] > 0 && sv.XrayConfigVer >= latest[sv.ID] && !sv.XrayDrift
	}
	deployErrCache := map[uint]string{}
	getDeployErr := func(sid uint) string {
		if msg, cached := deployErrCache[sid]; cached {
			return msg
		}
		msg := p.lastDeployError(sid)
		deployErrCache[sid] = msg
		return msg
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
		v.TodayBytes = todayBytes[id]
		v.MonthBytes = monthBytes[id]
		v.BackhaulMs = p.backhaulAvg(r.EntryServerID)
		v.LatencyMs = v.BackhaulMs
		if r.Type == "chain" {
			v.SegmentMs = p.segmentAvg(r.EntryServerID, r.LandingServerID)
			v.SegmentDown = p.segmentDown(r.EntryServerID, r.LandingServerID)
			v.LatencyMs += v.SegmentMs
		}
		switch {
		case !r.Enabled:
			v.Status = "disabled"
		case e == nil || e.Status == "offline" || !e.XrayRunning || e.Blocked:
			v.Status = "down"
		case v.SegmentDown:
			if len(parseIDs(r.BackupIDs)) > 0 {
				v.Status = "degraded"
			} else {
				v.Status = "down"
			}
		case r.Type == "chain" && byID[r.LandingServerID] != nil && byID[r.LandingServerID].Status == "offline":
			v.Status = "down"
		}
		v.ConfigSynced = synced(e)
		if r.Type == "chain" {
			if !synced(byID[r.LandingServerID]) {
				v.ConfigSynced = false
			}
			for _, bid := range parseIDs(r.BackupIDs) {
				if !synced(byID[bid]) {
					v.ConfigSynced = false
					break
				}
			}
		}
		if !v.ConfigSynced {
			sids := []uint{r.EntryServerID}
			if r.Type == "chain" {
				sids = append(sids, r.LandingServerID)
				sids = append(sids, parseIDs(r.BackupIDs)...)
			}
			for _, sid := range sids {
				if msg := getDeployErr(sid); sid != 0 && msg != "" {
					v.DeployError = msg
					break
				}
			}
		}
		if e != nil {
			if ri := riBy[e.ID]; ri != nil {
				v.ShareLink = VLESSLink(nodeFor(&r, e, byID[r.LandingServerID], ri, ""))
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
	if p.St.R().Where("server_id = ? AND type IN ? AND finished_at IS NOT NULL", serverID, []string{"apply_config", "xray_op"}).
		Order("id DESC").First(&t).Error != nil || t.Status == "success" {
		return ""
	}
	return lastLine(t.Output)
}
