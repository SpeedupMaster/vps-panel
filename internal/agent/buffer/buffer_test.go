package buffer

import (
	"testing"

	"vpspanel/internal/common/proto"
)

func TestBufferOrderedReplayAndAckOver1200Items(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}

	const total = 1350
	for i := 1; i <= total; i++ {
		d, err := b.Add(int64(1700000000+i), []proto.TrafficItem{{
			Scope: "server",
			Name:  "nic",
			Up:    uint64(i * 10),
			Down:  uint64(i * 20),
		}})
		if err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
		if d.Seq != uint64(i) {
			t.Fatalf("seq = %d, want %d", d.Seq, i)
		}
	}
	if got := b.Count(); got != total {
		t.Fatalf("Count = %d, want %d", got, total)
	}

	// Simulate partial ack (1..150), reopen across restart, then drain in batches of 200.
	b.Ack(150)
	if got := b.Count(); got != total-150 {
		t.Fatalf("Count after Ack(150) = %d, want %d", got, total-150)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	b, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	var seen []uint64
	for {
		batch := b.Pending(200)
		if len(batch) == 0 {
			break
		}
		for _, d := range batch {
			seen = append(seen, d.Seq)
		}
		b.Ack(batch[len(batch)-1].Seq)
	}

	if len(seen) != total-150 {
		t.Fatalf("replayed %d items, want %d", len(seen), total-150)
	}
	for i, seq := range seen {
		want := uint64(151 + i)
		if seq != want {
			t.Fatalf("seen[%d] = %d, want %d", i, seq, want)
		}
	}
	if got := b.Count(); got != 0 {
		t.Fatalf("Count after full drain = %d, want 0", got)
	}

	// Subsequent Add continues sequence monotonically after reopen.
	next, err := b.Add(1700009999, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Seq != total+1 {
		t.Fatalf("next seq = %d, want %d", next.Seq, total+1)
	}
}

func TestAddWithMetaAtomic(t *testing.T) {
	dir := t.TempDir()
	b, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	type counters struct{ RX, TX uint64 }
	d, err := b.AddWithMeta(1700001000, []proto.TrafficItem{{Scope: "server", Name: "nic", Up: 11, Down: 22}}, "nic", counters{RX: 100, TX: 200})
	if err != nil {
		t.Fatalf("AddWithMeta: %v", err)
	}
	if d.Seq != 1 {
		t.Fatalf("seq = %d, want 1", d.Seq)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}

	b, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	var got counters
	if !b.Get("nic", &got) || got.RX != 100 || got.TX != 200 {
		t.Fatalf("Get(nic) = %+v", got)
	}
	pending := b.Pending(10)
	if len(pending) != 1 || pending[0].Seq != 1 || len(pending[0].Items) != 1 || pending[0].Items[0].Up != 11 {
		t.Fatalf("Pending = %+v", pending)
	}
}
