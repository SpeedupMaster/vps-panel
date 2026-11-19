package service

import (
	"fmt"
	"testing"
)

func TestSubNodesPickedRoutes(t *testing.T) {
	p := newTestPanel(t)
	hk := addNode(t, p, "HK-01", "HK", "1.1.1.1", true, false)
	sg := addNode(t, p, "SG-01", "SG", "4.4.4.4", true, false)
	jp := addNode(t, p, "JP-01", "JP", "2.2.2.2", false, true)

	r1, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: hk.ID, Name: "hk"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.CreateRoute(RouteInput{Type: "chain", EntryServerID: hk.ID, LandingServerID: jp.ID, Name: "hk-jp"})
	if err != nil {
		t.Fatal(err)
	}
	r3, err := p.CreateRoute(RouteInput{Type: "direct", EntryServerID: sg.ID, Name: "sg"})
	if err != nil {
		t.Fatal(err)
	}

	count := func() int { return len(p.SubNodes()) }

	if n := count(); n != 3 {
		t.Fatalf("no picking: %d nodes, want 3", n)
	}

	p.St.Set("sub_routes", fmt.Sprintf("%d,%d", r2.ID, r3.ID))
	nodes := p.SubNodes()
	if len(nodes) != 2 || nodes[0].UUID != r2.UUID || nodes[1].UUID != r3.UUID {
		t.Fatalf("picked r2,r3: %+v", nodes)
	}

	// The blocked filter still applies on top of the picked set.
	p.St.DB.Model(sg).Update("blocked", true)
	if n := count(); n != 1 {
		t.Fatalf("picked + blocked: %d nodes, want 1", n)
	}

	// A picked route that leaves the subscription (in_sub off) stays out.
	p.St.DB.Model(r2).Update("in_sub", false)
	if n := count(); n != 0 {
		t.Fatalf("picked route not in_sub: %d nodes, want 0", n)
	}

	// Only deleted routes picked: nothing, not everything.
	p.St.Set("sub_routes", "999")
	if n := count(); n != 0 {
		t.Fatalf("stale pick: %d nodes, want 0", n)
	}

	// Cleared = every route again (minus the filters).
	p.St.Set("sub_routes", "")
	nodes = p.SubNodes()
	if len(nodes) != 1 || nodes[0].UUID != r1.UUID {
		t.Fatalf("cleared: %+v", nodes)
	}
}
