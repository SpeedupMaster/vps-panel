package service

import (
	"fmt"
	"strconv"
	"strings"

	"vpspanel/internal/panel/store"
	"vpspanel/internal/panel/sub"
)

// ---------- subscription node builder & userinfo ----------

func nodeFor(r *store.Route, entry, landing *store.Server, ri *store.RealityInbound, tpl string) sub.Node {
	if tpl == "" {
		tpl = "{flag} {name}"
	}
	flag := sub.Flag(entry.CountryCode)
	region := entry.CountryCode
	landingName := ""
	if r.Type == "chain" && landing != nil {
		flag += "→" + sub.Flag(landing.CountryCode)
		region = landing.CountryCode
		landingName = landing.Name
	}
	name := strings.NewReplacer("{flag}", flag, "{name}", r.Name, "{entry}", entry.Name, "{landing}", landingName).Replace(tpl)
	addr := ri.Address
	if addr == "" {
		addr = serverAddr(entry)
	}
	sni, _, _ := strings.Cut(ri.ServerNames, ",")
	sid, _, _ := strings.Cut(ri.ShortIDs, ",")
	fp := ri.Fingerprint
	if fp == "" {
		fp = "chrome"
	}
	return sub.Node{
		Name: strings.TrimSpace(name), Server: addr, Port: ri.Port, UUID: r.UUID, SNI: sni,
		PublicKey: ri.PublicKey, ShortID: sid, FP: fp, RegionCode: region,
		XrayVersion: entry.XrayVersion,
	}
}

func VLESSLink(n sub.Node) string { return sub.Link(n) }

// SubRouteIDs parses sub_routes: the global default route IDs picked for
// subscriptions. nil means no picking (every enabled in_sub route goes in).
func (p *Panel) SubRouteIDs() []uint {
	return parseRouteIDSpec(p.St.Get("sub_routes", ""))
}

func (p *Panel) effectiveRouteIDs(t *store.SubToken) []uint {
	if t != nil {
		if ids := parseRouteIDSpec(t.Routes); ids != nil {
			return ids
		}
	}
	return p.SubRouteIDs()
}

// SubNodes lists the routes that go into global default subscriptions.
func (p *Panel) SubNodes() []sub.Node {
	return p.SubNodesFor(nil)
}

// SubNodesFor lists the routes that go into subscriptions for token t
// (or global default when t is nil).
func (p *Panel) SubNodesFor(t *store.SubToken) []sub.Node {
	db := p.St.R()
	var rs []store.Route
	q := db.Where("enabled = ? AND in_sub = ?", true, true)
	if ids := p.effectiveRouteIDs(t); ids != nil {
		if len(ids) == 0 {
			return nil
		}
		q = q.Where("id IN ?", ids)
	}
	q.Order("sort, id").Find(&rs)
	var svs []store.Server
	db.Find(&svs)
	byID := map[uint]*store.Server{}
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}
	var ris []store.RealityInbound
	db.Find(&ris)
	riBy := map[uint]*store.RealityInbound{}
	for i := range ris {
		riBy[ris[i].ServerID] = &ris[i]
	}
	exBlocked := p.St.GetBool("sub_exclude_blocked", true)
	exOffline := p.St.GetBool("sub_exclude_offline", false)
	tpl := p.St.Get("sub_name_tpl", "{flag} {name}")
	seen := map[string]int{}
	for name := range sub.ReservedNames() {
		seen[name] = 1
	}
	var nodes []sub.Node
	for i := range rs {
		r := &rs[i]
		e, ri := byID[r.EntryServerID], riBy[r.EntryServerID]
		if e == nil || ri == nil || serverAddr(e) == "" && ri.Address == "" {
			continue
		}
		if exBlocked && e.Blocked || exOffline && e.Status == "offline" {
			continue
		}
		n := nodeFor(r, e, byID[r.LandingServerID], ri, tpl)
		seen[sub.RegionGroupName(n.RegionCode)] = 1
		base := n.Name
		if base == "" {
			base = "node"
		}
		cand := base
		for k := 1; seen[cand] > 0; {
			k++
			cand = fmt.Sprintf("%s %d", base, k)
		}
		n.Name = cand
		seen[cand]++
		nodes = append(nodes, n)
	}
	return nodes
}

func (p *Panel) subRouteServerIDsFor(t *store.SubToken) []uint {
	db := p.St.R()
	var rs []store.Route
	q := db.Where("enabled = ? AND in_sub = ?", true, true)
	if ids := p.effectiveRouteIDs(t); ids != nil {
		if len(ids) == 0 {
			return nil
		}
		q = q.Where("id IN ?", ids)
	}
	q.Find(&rs)
	if len(rs) == 0 {
		return nil
	}
	var svs []store.Server
	db.Where("mode = ?", "node").Find(&svs)
	byID := map[uint]*store.Server{}
	for i := range svs {
		byID[svs[i].ID] = &svs[i]
	}
	var ris []store.RealityInbound
	db.Find(&ris)
	riBy := map[uint]*store.RealityInbound{}
	for i := range ris {
		riBy[ris[i].ServerID] = &ris[i]
	}
	exBlocked := p.St.GetBool("sub_exclude_blocked", true)
	exOffline := p.St.GetBool("sub_exclude_offline", false)

	seen := map[uint]bool{}
	var ids []uint
	add := func(id uint) {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for i := range rs {
		r := &rs[i]
		e, ri := byID[r.EntryServerID], riBy[r.EntryServerID]
		if e == nil || ri == nil || serverAddr(e) == "" && ri.Address == "" {
			continue
		}
		if exBlocked && e.Blocked || exOffline && e.Status == "offline" {
			continue
		}
		add(r.EntryServerID)
		if r.Type == "chain" {
			add(r.LandingServerID)
			for _, b := range parseIDs(r.BackupIDs) {
				add(b)
			}
		}
	}
	return ids
}

func (p *Panel) subUserinfo() string {
	return p.subUserinfoFor(nil)
}

func (p *Panel) subUserinfoFor(t *store.SubToken) string {
	mode := p.St.Get("sub_userinfo", "all")
	if mode == "none" {
		return ""
	}
	db := p.St.R()
	var svs []store.Server
	switch {
	case mode == "sub" || (mode == "all" && p.effectiveRouteIDs(t) != nil):
		if ids := p.subRouteServerIDsFor(t); len(ids) > 0 {
			db.Where("mode = ? AND id IN ?", "node", ids).Find(&svs)
		}
	default:
		if id, err := strconv.Atoi(mode); err == nil {
			db.Where("id = ?", id).Find(&svs)
		} else {
			db.Where("mode = ?", "node").Find(&svs)
		}
	}
	usageBy := p.CycleUsageMap(svs)
	var up, down, total uint64
	var expire int64
	for i := range svs {
		u := usageBy[svs[i].ID]
		up, down = up+u.Up, down+u.Down
		if svs[i].QuotaBytes > 0 {
			total += uint64(svs[i].QuotaBytes)
		}
		if svs[i].ExpireAt != nil && (expire == 0 || svs[i].ExpireAt.Unix() < expire) && svs[i].Mode == "node" {
			expire = svs[i].ExpireAt.Unix()
		}
	}
	return sub.Userinfo(up, down, total, expire)
}
