package agent

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"vpspanel/internal/agent/buffer"
	"vpspanel/internal/agent/config"
	"vpspanel/internal/common/proto"
)

func TestMergePanelURLsKeepsWorkingAddress(t *testing.T) {
	got := mergePanelURLs([]string{"https://panel.example.com/", "https://backup.example.com"}, "http://172.17.0.1:18080")
	want := []string{"https://panel.example.com", "https://backup.example.com", "http://172.17.0.1:18080"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
	got = mergePanelURLs([]string{"https://panel.example.com"}, "https://panel.example.com/")
	if len(got) != 1 {
		t.Fatalf("duplicate kept: %v", got)
	}
	if got = mergePanelURLs(nil, "http://a"); !reflect.DeepEqual(got, []string{"http://a"}) {
		t.Fatalf("empty list: %v", got)
	}
}

func TestRandomNonce(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		n, err := randomNonce()
		if err != nil {
			t.Fatal(err)
		}
		if len(n) != 32 {
			t.Fatalf("nonce len = %d, want 32", len(n))
		}
		if _, err := hex.DecodeString(n); err != nil {
			t.Fatalf("nonce not hex: %q (%v)", n, err)
		}
		if seen[n] {
			t.Fatalf("duplicate nonce: %s", n)
		}
		seen[n] = true
	}
}

type fakeHub struct {
	mu           sync.Mutex
	secret       string
	received     map[uint64]bool
	order        []uint64
	lastAccepted uint64
	nonces       []string
	// Disconnect once after acknowledging each of these sequence numbers.
	dropAfter map[uint64]bool
	// Callback invoked once when a specific seq is received.
	onSeq map[uint64]func()
	// Closed once targetSeq is acknowledged.
	targetSeq uint64
	done      chan struct{}
	doneOnce  sync.Once
}

func (h *fakeHub) Register(context.Context, *proto.RegisterRequest) (*proto.RegisterResponse, error) {
	return nil, errors.New("unused")
}

func (h *fakeHub) Connect(stream grpc.ServerStream) error {
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok {
		return errors.New("missing metadata")
	}
	get := func(k string) string {
		if v := md.Get(k); len(v) > 0 {
			return v[0]
		}
		return ""
	}
	agentID, tsStr, nonce, sig := get(proto.MDAgentID), get(proto.MDTime), get(proto.MDNonce), get(proto.MDSig)
	ts, err := strconv.ParseInt(tsStr, 10, 64)
	if err != nil || len(nonce) != 32 || proto.Sign(h.secret, agentID, ts, nonce) != sig {
		return errors.New("invalid handshake signature or nonce")
	}
	h.mu.Lock()
	h.nonces = append(h.nonces, nonce)
	h.mu.Unlock()

	for {
		m := new(proto.AgentMessage)
		if err := stream.RecvMsg(m); err != nil {
			return err
		}
		if m.Traffic == nil {
			continue
		}
		seq := m.Traffic.Seq
		var cb func()
		var drop bool
		h.mu.Lock()
		if seq > h.lastAccepted {
			h.lastAccepted = seq
			h.received[seq] = true
			h.order = append(h.order, seq)
		}
		if fn, ok := h.onSeq[seq]; ok {
			delete(h.onSeq, seq)
			cb = fn
		}
		if h.dropAfter[seq] {
			delete(h.dropAfter, seq)
			drop = true
		}
		reached := seq >= h.targetSeq
		h.mu.Unlock()

		if err := stream.SendMsg(&proto.PanelMessage{Ack: &proto.Ack{Seq: seq}}); err != nil {
			return err
		}
		if cb != nil {
			cb()
		}
		if reached {
			h.doneOnce.Do(func() { close(h.done) })
		}
		if drop {
			// Give the client a brief moment to process Ack(seq), then break stream.
			time.Sleep(25 * time.Millisecond)
			return errors.New("simulated disconnect")
		}
	}
}

func TestLosslessTrafficReplayAcrossReconnects(t *testing.T) {
	buf, err := buffer.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer buf.Close()

	const initialOffline = 1250
	for i := 1; i <= initialOffline; i++ {
		if _, err := buf.Add(int64(1700000000+i), []proto.TrafficItem{{
			Scope: "server", Name: "nic", Up: uint64(i), Down: uint64(i * 2),
		}}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	const secret = "test-agent-secret"
	hub := &fakeHub{
		secret:    secret,
		received:  map[uint64]bool{},
		dropAfter: map[uint64]bool{150: true, 650: true},
		onSeq:     map[uint64]func(){},
		targetSeq: initialOffline + 3,
		done:      make(chan struct{}),
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := grpc.NewServer()
	srv.RegisterService(&proto.ServiceDesc, hub)
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	a := New(&config.Credentials{
		PanelURLs:   []string{"http://" + lis.Addr().String()},
		AgentID:     "agent-1",
		AgentSecret: secret,
		ServerName:  "vps-test",
	}, config.Local{Mode: "probe"}, buf)

	// Simulate live trafficLoop appending new deltas while offline replay is still in progress.
	hub.onSeq[50] = func() {
		_, _ = buf.Add(1700005001, []proto.TrafficItem{{Scope: "server", Name: "nic", Up: 1}})
		a.notifyTraffic()
	}
	hub.onSeq[300] = func() {
		_, _ = buf.Add(1700005002, []proto.TrafficItem{{Scope: "server", Name: "nic", Up: 2}})
		a.notifyTraffic()
	}
	hub.onSeq[900] = func() {
		_, _ = buf.Add(1700005003, []proto.TrafficItem{{Scope: "server", Name: "nic", Up: 3}})
		a.notifyTraffic()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// Drive sessions across the simulated disconnects until all items replay.
	go func() {
		for ctx.Err() == nil {
			_ = a.session(ctx, a.Creds.PanelURLs[0])
		}
	}()

	select {
	case <-hub.done:
	case <-ctx.Done():
		hub.mu.Lock()
		got := len(hub.received)
		hub.mu.Unlock()
		t.Fatalf("timed out waiting for full replay: received %d / %d, buf remaining %d", got, hub.targetSeq, buf.Count())
	}

	// Wait briefly for final Ack to be flushed into bbolt on the client side.
	deadline := time.Now().Add(2 * time.Second)
	for buf.Count() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()

	hub.mu.Lock()
	defer hub.mu.Unlock()
	if uint64(len(hub.received)) != hub.targetSeq {
		t.Fatalf("received %d unique deltas, want %d", len(hub.received), hub.targetSeq)
	}
	for i := uint64(1); i <= hub.targetSeq; i++ {
		if !hub.received[i] {
			t.Fatalf("missing seq %d in replayed deltas", i)
		}
	}
	if uint64(len(hub.order)) != hub.targetSeq {
		t.Fatalf("order len = %d, want %d", len(hub.order), hub.targetSeq)
	}
	for i, seq := range hub.order {
		if want := uint64(i + 1); seq != want {
			t.Fatalf("out-of-order replay at index %d: got seq %d, want %d", i, seq, want)
		}
	}
	if len(hub.nonces) < 3 {
		t.Fatalf("expected at least 3 reconnect sessions, got %d", len(hub.nonces))
	}
	if buf.Count() != 0 {
		t.Fatalf("expected empty buffer after full replay, remaining %d", buf.Count())
	}
}
