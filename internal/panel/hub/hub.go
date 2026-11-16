// Package hub accepts agent connections (gRPC bidi stream) and routes
// messages between agents and the panel services.
package hub

import (
	"context"
	"crypto/hmac"
	"errors"
	"log"
	"strconv"
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

type Hub struct {
	st  *store.Store
	box *secret.Box
	h   Handler

	mu    sync.RWMutex
	conns map[uint]*Conn
}

func New(st *store.Store, box *secret.Box) *Hub {
	return &Hub{st: st, box: box, conns: map[uint]*Conn{}}
}

func (h *Hub) SetHandler(handler Handler) { h.h = handler }

// GRPCServer builds the server with the AgentHub service registered.
func (h *Hub) GRPCServer() *grpc.Server {
	s := grpc.NewServer(grpc.MaxRecvMsgSize(32 << 20))
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
	var sv store.Server
	if err := h.st.DB.First(&sv, "install_token_hash = ?", secret.Hash(req.Token)).Error; err != nil {
		return nil, status.Error(codes.PermissionDenied, "invalid token")
	}
	if time.Now().After(sv.InstallTokenExp) {
		return nil, status.Error(codes.PermissionDenied, "token expired")
	}
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
	if p, ok := peer.FromContext(ctx); ok {
		upd["ipv4"] = hostOnly(p.Addr.String())
	}
	if err := h.st.DB.Model(&sv).Updates(upd).Error; err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
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
	c := &Conn{ServerID: sv.ID, AgentID: sv.AgentID, Since: time.Now(), send: make(chan *proto.PanelMessage, 256), cancel: cancel}
	if p, ok := peer.FromContext(stream.Context()); ok {
		c.RemoteIP = hostOnly(p.Addr.String())
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
		if h.conns[sv.ID] == c {
			delete(h.conns, sv.ID)
		}
		h.mu.Unlock()
		if h.h != nil {
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
	return &sv, nil
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
