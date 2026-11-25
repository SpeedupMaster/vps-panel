package service

import "vpspanel/internal/panel/store"

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
	p.St.R().Where("mode = ?", "node").Order("sort, id").Find(&svs)
	rolesBy := p.RolesMap(svs)
	nodes := make([]TopoNode, 0, len(svs))
	for i := range svs {
		sv := &svs[i]
		nodes = append(nodes, TopoNode{sv.ID, sv.Name, sv.CountryCode, rolesBy[sv.ID], sv.Status, sv.Blocked, p.backhaulAvg(sv.ID)})
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
