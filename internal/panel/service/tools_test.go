package service

import (
	"testing"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/store"
)

func TestSyncNetState(t *testing.T) {
	p := newTestPanel(t)
	sv := addNode(t, p, "n1", "US", "203.0.113.5", true, false)
	p.St.DB.Model(sv).Update("system_json", `{"arch":"amd64","congestion":"cubic","qdisc":"fq_codel","avail_cc":"reno cubic"}`)
	sys := func() proto.SystemInfo {
		var s store.Server
		p.St.DB.First(&s, sv.ID)
		return SystemInfo(&s)
	}

	// tune_net nests the state under "status"; a failed run still reports it.
	p.afterToolTask(&store.Task{ServerID: sv.ID, Type: proto.TaskTuneNet}, &proto.TaskResult{Status: "failed", Data: map[string]any{
		"diff": []string{}, "status": map[string]any{"congestion": "bbr", "qdisc": "fq", "available": "reno cubic bbr"},
	}})
	if s := sys(); s.Congestion != "bbr" || s.Qdisc != "fq" || s.AvailCC != "reno cubic bbr" || s.Arch != "amd64" {
		t.Fatalf("after tune: %+v", s)
	}

	// restore_net returns the state itself.
	p.afterToolTask(&store.Task{ServerID: sv.ID, Type: proto.TaskRestoreNet}, &proto.TaskResult{Status: "success", Data: map[string]any{
		"congestion": "cubic", "qdisc": "fq_codel", "available": "reno cubic bbr",
	}})
	if s := sys(); s.Congestion != "cubic" || s.Qdisc != "fq_codel" {
		t.Fatalf("after restore: %+v", s)
	}

	// A dry run carries no state and must not wipe what is there.
	p.afterToolTask(&store.Task{ServerID: sv.ID, Type: proto.TaskTuneNet}, &proto.TaskResult{Status: "success", Data: map[string]any{"diff": []string{"+ x = 1"}}})
	if s := sys(); s.Congestion != "cubic" {
		t.Fatalf("after dry run: %+v", s)
	}
}
