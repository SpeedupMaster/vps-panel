// Package buffer persists unacknowledged traffic deltas in bbolt so nothing
// is lost while the panel is unreachable or the agent restarts.
package buffer

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"vpspanel/internal/common/proto"
)

var (
	bTraffic = []byte("traffic")
	bMeta    = []byte("meta")
	kSeq     = []byte("seq")
)

type Buffer struct{ db *bolt.DB }

func Open(dir string) (*Buffer, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(dir, "buffer.db"), 0o600, &bolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists(bTraffic); err != nil {
			return err
		}
		_, err := tx.CreateBucketIfNotExists(bMeta)
		return err
	})
	return &Buffer{db: db}, err
}

func (b *Buffer) Close() error { return b.db.Close() }

func key(seq uint64) []byte {
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, seq)
	return k
}

// Add assigns the next sequence number and stores the delta.
func (b *Buffer) Add(ts int64, items []proto.TrafficItem) (*proto.TrafficDelta, error) {
	var d *proto.TrafficDelta
	err := b.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(bMeta)
		var seq uint64
		if v := meta.Get(kSeq); v != nil {
			seq = binary.BigEndian.Uint64(v)
		}
		seq++
		if err := meta.Put(kSeq, key(seq)); err != nil {
			return err
		}
		d = &proto.TrafficDelta{Seq: seq, TS: ts, Items: items}
		raw, _ := json.Marshal(d)
		bk := tx.Bucket(bTraffic)
		// Cap the backlog (~1 week at 1/min) to bound disk use.
		if bk.Stats().KeyN > 10000 {
			c := bk.Cursor()
			if k, _ := c.First(); k != nil {
				_ = bk.Delete(k)
			}
		}
		return bk.Put(key(seq), raw)
	})
	return d, err
}

// Ack drops everything up to and including seq.
func (b *Buffer) Ack(seq uint64) {
	_ = b.db.Update(func(tx *bolt.Tx) error {
		bk := tx.Bucket(bTraffic)
		c := bk.Cursor()
		for {
			k, _ := c.First()
			if k == nil || binary.BigEndian.Uint64(k) > seq {
				break
			}
			if err := c.Delete(); err != nil {
				return err
			}
		}
		return nil
	})
}

// Count returns the number of unacknowledged traffic deltas in the buffer.
func (b *Buffer) Count() int {
	n := 0
	_ = b.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bTraffic).Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			n++
		}
		return nil
	})
	return n
}

// Pending returns unacknowledged deltas in order.
func (b *Buffer) Pending(limit int) []*proto.TrafficDelta {
	var out []*proto.TrafficDelta
	_ = b.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket(bTraffic).Cursor()
		for k, v := c.First(); k != nil && len(out) < limit; k, v = c.Next() {
			var d proto.TrafficDelta
			if json.Unmarshal(v, &d) == nil {
				out = append(out, &d)
			}
		}
		return nil
	})
	return out
}

// Put/Get small state values (e.g. last NIC counters).
func (b *Buffer) Put(k string, v any) {
	raw, _ := json.Marshal(v)
	_ = b.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(bMeta).Put([]byte(k), raw) })
}

func (b *Buffer) Get(k string, v any) bool {
	found := false
	_ = b.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket(bMeta).Get([]byte(k))
		if raw != nil {
			found = json.Unmarshal(raw, v) == nil
		}
		return nil
	})
	return found
}
