// Package hub accepts agent connections (gRPC bidi stream) and routes
// messages between agents and the panel services.
package hub

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
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

// Handler receives agent traffic; implemented by the service layer.
type Handler interface {
	OnConnect(c *Conn)
	OnDisconnect(c *Conn)
	OnMessage(c *Conn, m *proto.AgentMessage)
}

type Conn struct {
	ServerID uint
	AgentID  string
	RemoteIP string
	Since    time.Time
	send     chan *proto.PanelMessage
	cancel   context.CancelFunc

	mu    sync.RWMutex
	hello *proto.Hello
}

func (c *Conn) Hello() *proto.Hello {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.hello
}

func (c *Conn) SetHello(h *proto.Hello) {
	c.mu.Lock()
	c.hello = h
	c.mu.Unlock()
}

const maxHandshakeNonces = 16384

type Hub struct {
	st  *store.Store
	box *secret.Box
	h   Handler

	mu    sync.RWMutex
	conns map[uint]*Conn

	nonceMu sync.Mutex
	nonces  map[string]time.Time // "agentID\x00nonce" -> expiresAt

	trustedMu   sync.RWMutex
	trustedIPs  []net.IP
	trustedNets []*net.IPNet
}

func New(st *store.Store, box *secret.Box) *Hub {
	h := &Hub{
		st:     st,
		box:    box,
		conns:  map[uint]*Conn{},
		nonces: map[string]time.Time{},
	}
	h.SetTrustedProxies([]string{"127.0.0.1", "::1"})
	return h
}

// SetTrustedProxies configures reverse proxy IPs/CIDRs allowed to supply
// X-Forwarded-For / X-Real-IP headers on incoming gRPC connections.
func (h *Hub) SetTrustedProxies(proxies []string) {
	var ips []net.IP
	var nets []*net.IPNet
	for _, raw := range proxies {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			if _, ipnet, err := net.ParseCIDR(s); err == nil {
				nets = append(nets, ipnet)
			}
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			ips = append(ips, ip)
		}
	}
	h.trustedMu.Lock()
	h.trustedIPs = ips
	h.trustedNets = nets
	h.trustedMu.Unlock()
}

func (h *Hub) isTrustedProxy(ipStr string) bool {
	ip := net.ParseIP(strings.TrimSpace(ipStr))
	if ip == nil {
		return false
	}
	h.trustedMu.RLock()
	defer h.trustedMu.RUnlock()
	for _, tip := range h.trustedIPs {
		if tip.Equal(ip) {
			return true
		}
	}
	for _, n := range h.trustedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func (h *Hub) remoteIP(ctx context.Context) string {
	var peerIP string
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		peerIP = hostOnly(p.Addr.String())
	}
	if peerIP == "" || !h.isTrustedProxy(peerIP) {
		return peerIP
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return peerIP
	}
	if vals := md.Get("x-forwarded-for"); len(vals) > 0 {
		parts := strings.Split(strings.Join(vals, ","), ",")
		var fallback string
		for i := len(parts) - 1; i >= 0; i-- {
			cand := strings.TrimSpace(parts[i])
			if net.ParseIP(cand) == nil {
				continue
			}
			fallback = cand
			if !h.isTrustedProxy(cand) {
				return cand
			}
		}
		if fallback != "" {
			return fallback
		}
	}
	if vals := md.Get("x-real-ip"); len(vals) > 0 {
		if cand := strings.TrimSpace(vals[0]); net.ParseIP(cand) != nil {
			return cand
		}
	}
	return peerIP
}

var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

func isPublicIPv4(s string) bool {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return false
	}
	return !addr.IsLoopback() && !addr.IsPrivate() && !addr.IsLinkLocalUnicast() &&
		!addr.IsLinkLocalMulticast() && !addr.IsUnspecified() && !addr.IsMulticast() &&
		!cgnatPrefix.Contains(addr)
}

// Disconnect closes and removes any live stream for serverID without firing
// OnDisconnect (used when deleting or re-registering a server).
func (h *Hub) Disconnect(serverID uint) {
	h.mu.Lock()
	c := h.conns[serverID]
	if c != nil {
		delete(h.conns, serverID)
	}
	h.mu.Unlock()
	if c != nil {
		c.cancel()
	}
}

func (h *Hub) SetHandler(handler Handler) { h.h = handler }

// GRPCServer builds the server with the AgentHub service registered.
func (h *Hub) GRPCServer() *grpc.Server {
	s := grpc.NewServer(
		grpc.MaxRecvMsgSize(8<<20),
		grpc.ReadBufferSize(4<<10),
		grpc.WriteBufferSize(4<<10),
		grpc.SharedWriteBuffer(true),
	)
	s.RegisterService(&proto.ServiceDesc, h)
	return s
}

var ErrOffline = errors.New("agent offline")

// Send queues a message for the agent; it never blocks for long.
func (h *Hub) Send(serverID uint, m *proto.PanelMessage) error {
	h.mu.RLock()
	c := h.conns[serverID]
	h.mu.RUnlock()
	if c == nil {
		return ErrOffline
	}
	select {
	case c.send <- m:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("agent send queue full")
	}
}

func (h *Hub) Conn(serverID uint) *Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.conns[serverID]
}

func (h *Hub) Online(serverID uint) bool { return h.Conn(serverID) != nil }

func (h *Hub) OnlineIDs() []uint {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]uint, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	return ids
}

// Register exchanges a one-time install token for long-lived credentials.
func (h *Hub) Register(ctx context.Context, req *proto.RegisterRequest) (*proto.RegisterResponse, error) {
	if req.Token == "" {
		return nil, status.Error(codes.InvalidArgument, "token required")
	}
	hash := secret.Hash(req.Token)
	var sv store.Server
	if err := h.st.DB.First(&sv, "install_token_hash = ?", hash).Error; err != nil {
		return nil, status.Error(codes.PermissionDenied, "invalid token")
	}
	if time.Now().After(sv.InstallTokenExp) {
		return nil, status.Error(codes.PermissionDenied, "token expired")
	}
	oldAgentID := sv.AgentID
	agentID := "a-" + secret.RandomToken(9)
	agentSecret := secret.RandomToken(32)
	upd := map[string]any{
		"agent_id":           agentID,
		"agent_secret_enc":   h.box.Seal(agentSecret),
		"install_token_hash": "",
		"status":             "offline",
	}
	if req.Mode == "probe" || req.Mode == "node" {
		upd["mode"] = req.Mode
	}
	if ip := strings.TrimSpace(req.System.IPv4); ip != "" {
		upd["ipv4"] = ip
	} else if rip := h.remoteIP(ctx); isPublicIPv4(rip) {
		upd["ipv4"] = rip
	}
	if ip := strings.TrimSpace(req.System.IPv6); ip != "" {
		upd["ipv6"] = ip
	}
	if req.System != (proto.SystemInfo{}) {
		if sysJSON, err := json.Marshal(req.System); err == nil {
			upd["system_json"] = string(sysJSON)
		}
	}
	res := h.st.DB.Model(&store.Server{}).
		Where("id = ? AND install_token_hash = ?", sv.ID, hash).
		Updates(upd)
	if res.Error != nil {
		return nil, status.Error(codes.Internal, res.Error.Error())
	}
	if res.RowsAffected != 1 {
		return nil, status.Error(codes.PermissionDenied, "invalid token")
	}
	if oldAgentID != "" {
		h.st.DB.Delete(&store.TrafficSeq{}, "agent_id = ?", oldAgentID)
	}
	h.Disconnect(sv.ID)
	log.Printf("hub: server %s registered as %s", sv.Name, agentID)
	return &proto.RegisterResponse{AgentID: agentID, AgentSecret: agentSecret, ServerName: sv.Name}, nil
}

// Connect is the long-lived bidirectional stream.
func (h *Hub) Connect(stream grpc.ServerStream) error {
	sv, err := h.authenticate(stream.Context())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(stream.Context())
	c := &Conn{
		ServerID: sv.ID,
		AgentID:  sv.AgentID,
		RemoteIP: h.remoteIP(stream.Context()),
		Since:    time.Now(),
		send:     make(chan *proto.PanelMessage, 64),
		cancel:   cancel,
	}

	h.mu.Lock()
	if old := h.conns[sv.ID]; old != nil {
		old.cancel() // newer connection wins
	}
	h.conns[sv.ID] = c
	h.mu.Unlock()

	defer func() {
		cancel()
		h.mu.Lock()
		wasActive := h.conns[sv.ID] == c
		if wasActive {
			delete(h.conns, sv.ID)
		}
		h.mu.Unlock()
		if wasActive && h.h != nil {
			h.h.OnDisconnect(c)
		}
	}()

	errc := make(chan error, 2)
	go func() {
		for {
			select {
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			case m := <-c.send:
				if err := stream.SendMsg(m); err != nil {
					errc <- err
					return
				}
			}
		}
	}()
	go func() {
		first := true
		for {
			m := new(proto.AgentMessage)
			if err := stream.RecvMsg(m); err != nil {
				errc <- err
				return
			}
			if m.Hello != nil {
				c.SetHello(m.Hello)
				if first && h.h != nil {
					first = false
					h.h.OnConnect(c)
				}
			}
			if h.h != nil {
				h.h.OnMessage(c, m)
			}
		}
	}()
	err = <-errc
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (h *Hub) authenticate(ctx context.Context) (*store.Server, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	get := func(k string) string {
		if v := md.Get(k); len(v) > 0 {
			return v[0]
		}
		return ""
	}
	id, tsStr, nonce, sig := get(proto.MDAgentID), get(proto.MDTime), get(proto.MDNonce), get(proto.MDSig)
	ts, _ := strconv.ParseInt(tsStr, 10, 64)
	if id == "" || nonce == "" || sig == "" || abs(time.Now().Unix()-ts) > 300 {
		return nil, status.Error(codes.Unauthenticated, "bad handshake")
	}
	var sv store.Server
	if err := h.st.DB.First(&sv, "agent_id = ?", id).Error; err != nil {
		return nil, status.Error(codes.Unauthenticated, "unknown agent")
	}
	sec, err := h.box.Open(sv.AgentSecretEnc)
	if err != nil || !hmac.Equal([]byte(proto.Sign(sec, id, ts, nonce)), []byte(sig)) {
		return nil, status.Error(codes.Unauthenticated, "bad signature")
	}
	if !h.checkAndRememberNonce(id, nonce, ts) {
		return nil, status.Error(codes.Unauthenticated, "replayed handshake")
	}
	return &sv, nil
}

func (h *Hub) checkAndRememberNonce(agentID, nonce string, ts int64) bool {
	now := time.Now()
	key := agentID + "\x00" + nonce
	exp := time.Unix(ts+305, 0)
	if exp.Before(now.Add(5 * time.Second)) {
		exp = now.Add(5 * time.Second)
	}
	h.nonceMu.Lock()
	defer h.nonceMu.Unlock()
	if h.nonces == nil {
		h.nonces = map[string]time.Time{}
	}
	if prevExp, exists := h.nonces[key]; exists && now.Before(prevExp) {
		return false
	}
	if len(h.nonces) >= 1024 {
		for k, e := range h.nonces {
			if !now.Before(e) {
				delete(h.nonces, k)
			}
		}
	}
	for len(h.nonces) >= maxHandshakeNonces {
		var oldestKey string
		var oldestExp time.Time
		for k, e := range h.nonces {
			if oldestKey == "" || e.Before(oldestExp) {
				oldestKey, oldestExp = k, e
			}
		}
		if oldestKey == "" {
			break
		}
		delete(h.nonces, oldestKey)
	}
	h.nonces[key] = exp
	return true
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

func hostOnly(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			h := addr[:i]
			if len(h) > 1 && h[0] == '[' {
				h = h[1 : len(h)-1]
			}
			return h
		}
	}
	return addr
}
