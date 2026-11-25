package hub

import (
	"context"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/secret"
	"vpspanel/internal/panel/store"
)

func newTestHub(t *testing.T) (*Hub, *store.Store, *secret.Box) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	box, err := secret.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return New(st, box), st, box
}

func createPendingServer(t *testing.T, st *store.Store, name, token string) *store.Server {
	t.Helper()
	sv := &store.Server{
		Name:             name,
		Mode:             "node",
		Status:           "pending",
		AgentID:          "pending-" + secret.RandomToken(6),
		InstallTokenHash: secret.Hash(token),
		InstallTokenExp:  time.Now().Add(time.Hour),
	}
	if err := st.DB.Create(sv).Error; err != nil {
		t.Fatal(err)
	}
	return sv
}

func TestRegisterAtomicAndSystemIP(t *testing.T) {
	h, st, _ := newTestHub(t)
	tok := "install-tok-1"
	sv := createPendingServer(t, st, "S1", tok)
	oldAgent := sv.AgentID
	// Seed an old TrafficSeq row to verify Register clears it.
	st.DB.Create(&store.TrafficSeq{AgentID: oldAgent, LastSeq: 99})

	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}})

	// Concurrent Register calls with same token: exactly one succeeds.
	var okCount atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.Register(ctx, &proto.RegisterRequest{
				Token: tok,
				Mode:  "node",
				System: proto.SystemInfo{
					Hostname: "vps1",
					IPv4:     "203.0.113.10",
					IPv6:     "2001:db8::10",
				},
			})
			if err == nil {
				okCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if okCount.Load() != 1 {
		t.Fatalf("concurrent Register successes = %d, want 1", okCount.Load())
	}

	var updated store.Server
	st.DB.First(&updated, sv.ID)
	if updated.IPv4 != "203.0.113.10" || updated.IPv6 != "2001:db8::10" {
		t.Fatalf("got ipv4=%q ipv6=%q", updated.IPv4, updated.IPv6)
	}
	if updated.SystemJSON == "" {
		t.Fatal("system_json was not populated from req.System")
	}
	var seqCount int64
	st.DB.Model(&store.TrafficSeq{}).Where("agent_id = ?", oldAgent).Count(&seqCount)
	if seqCount != 0 {
		t.Fatalf("old TrafficSeq not cleared on Register")
	}

	// Register from loopback/private peer without req.System.IPv4 must NOT write 127.0.0.1/172.x.
	tok2 := "install-tok-2"
	sv2 := createPendingServer(t, st, "S2", tok2)
	privCtx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("172.17.0.2"), Port: 9999}})
	if _, err := h.Register(privCtx, &proto.RegisterRequest{Token: tok2, Mode: "probe"}); err != nil {
		t.Fatalf("Register S2: %v", err)
	}
	var updated2 store.Server
	if err := st.DB.First(&updated2, sv2.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated2.IPv4 != "" {
		t.Fatalf("private peer IP leaked into servers.ipv4: %q", updated2.IPv4)
	}
}

func TestAuthenticateReplayCache(t *testing.T) {
	h, st, _ := newTestHub(t)
	tok := "replay-tok"
	createPendingServer(t, st, "R1", tok)
	resp, err := h.Register(context.Background(), &proto.RegisterRequest{Token: tok, Mode: "probe"})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Unix()
	nonce := "nonce-abc-123"
	sig := proto.Sign(resp.AgentSecret, resp.AgentID, ts, nonce)
	md := metadata.Pairs(
		proto.MDAgentID, resp.AgentID,
		proto.MDTime, strconv.FormatInt(ts, 10),
		proto.MDNonce, nonce,
		proto.MDSig, sig,
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	if _, err := h.authenticate(ctx); err != nil {
		t.Fatalf("first authenticate failed: %v", err)
	}
	if _, err := h.authenticate(ctx); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("replayed authenticate = %v, want Unauthenticated", err)
	}
}

func TestRemoteIPTrustedProxies(t *testing.T) {
	h, _, _ := newTestHub(t)
	h.SetTrustedProxies([]string{"127.0.0.1", "10.0.0.0/8"})

	// From trusted proxy: extract rightmost untrusted X-Forwarded-For IP.
	md := metadata.Pairs("x-forwarded-for", "1.1.1.1, 198.51.100.5, 10.0.0.2")
	ctxTrusted := metadata.NewIncomingContext(
		peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}}),
		md,
	)
	if got := h.remoteIP(ctxTrusted); got != "198.51.100.5" {
		t.Fatalf("trusted proxy remoteIP = %q, want 198.51.100.5", got)
	}

	// From untrusted peer: ignore X-Forwarded-For completely.
	ctxUntrusted := metadata.NewIncomingContext(
		peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("203.0.113.9"), Port: 1234}}),
		md,
	)
	if got := h.remoteIP(ctxUntrusted); got != "203.0.113.9" {
		t.Fatalf("untrusted peer remoteIP = %q, want 203.0.113.9", got)
	}
}

type mockStream struct {
	grpc.ServerStream
	ctx   context.Context
	block chan struct{}
}

func (m *mockStream) Context() context.Context { return m.ctx }
func (m *mockStream) SendMsg(any) error        { return nil }
func (m *mockStream) RecvMsg(any) error {
	<-m.block
	return context.Canceled
}

type countHandler struct {
	disconnects atomic.Int32
}

func (c *countHandler) OnConnect(*Conn)                     {}
func (c *countHandler) OnDisconnect(*Conn)                  { c.disconnects.Add(1) }
func (c *countHandler) OnMessage(*Conn, *proto.AgentMessage) {}

func TestReconnectRaceAndDisconnect(t *testing.T) {
	h, st, _ := newTestHub(t)
	ch := &countHandler{}
	h.SetHandler(ch)

	tok := "race-tok"
	sv := createPendingServer(t, st, "Race1", tok)
	resp, err := h.Register(context.Background(), &proto.RegisterRequest{Token: tok, Mode: "probe"})
	if err != nil {
		t.Fatal(err)
	}

	mkStream := func(nonce string) *mockStream {
		ts := time.Now().Unix()
		sig := proto.Sign(resp.AgentSecret, resp.AgentID, ts, nonce)
		md := metadata.Pairs(
			proto.MDAgentID, resp.AgentID,
			proto.MDTime, strconv.FormatInt(ts, 10),
			proto.MDNonce, nonce,
			proto.MDSig, sig,
		)
		return &mockStream{
			ctx:   metadata.NewIncomingContext(context.Background(), md),
			block: make(chan struct{}),
		}
	}

	s1 := mkStream("n1")
	done1 := make(chan error, 1)
	go func() { done1 <- h.Connect(s1) }()
	for !h.Online(sv.ID) {
		time.Sleep(5 * time.Millisecond)
	}

	// Second stream supersedes s1; s2 cancels s1 and s1's defer must NOT invoke OnDisconnect.
	s2 := mkStream("n2")
	done2 := make(chan error, 1)
	go func() { done2 <- h.Connect(s2) }()
	// Wait for s1 to exit from being cancelled by s2.
	<-done1
	close(s1.block)
	if ch.disconnects.Load() != 0 {
		t.Fatalf("superseded stream fired OnDisconnect (%d times)", ch.disconnects.Load())
	}
	if !h.Online(sv.ID) {
		t.Fatal("server should remain online on s2")
	}

	// Explicit Disconnect removes connection without firing OnDisconnect.
	h.Disconnect(sv.ID)
	<-done2
	close(s2.block)
	if ch.disconnects.Load() != 0 {
		t.Fatalf("Hub.Disconnect should not fire OnDisconnect, got %d", ch.disconnects.Load())
	}
	if h.Online(sv.ID) {
		t.Fatal("server should be offline after Disconnect")
	}
}
