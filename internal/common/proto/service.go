package proto

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/mem"
)

// CodecName is the gRPC content-subtype used by both sides.
const CodecName = "json"

type jsonCodec struct{}

func (jsonCodec) Marshal(v any) (mem.BufferSlice, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mem.BufferSlice{mem.SliceBuffer(b)}, nil
}

func (jsonCodec) Unmarshal(data mem.BufferSlice, v any) error {
	return json.Unmarshal(data.Materialize(), v)
}

func (jsonCodec) Name() string { return CodecName }

func init() { encoding.RegisterCodecV2(jsonCodec{}) }

// CallOption makes a client call use the JSON codec.
func CallOption() grpc.CallOption { return grpc.CallContentSubtype(CodecName) }

const (
	ServiceName    = "vpspanel.AgentHub"
	MethodRegister = "/" + ServiceName + "/Register"
	MethodConnect  = "/" + ServiceName + "/Connect"
)

// Metadata keys used for the HMAC handshake on Connect.
const (
	MDAgentID = "x-agent-id"
	MDTime    = "x-agent-ts"
	MDNonce   = "x-agent-nonce"
	MDSig     = "x-agent-sig"
)

type RegisterRequest struct {
	Token    string     `json:"token"`
	Mode     string     `json:"mode"`
	Hostname string     `json:"hostname"`
	System   SystemInfo `json:"system"`
}

type RegisterResponse struct {
	AgentID     string `json:"agent_id"`
	AgentSecret string `json:"agent_secret"`
	ServerName  string `json:"server_name"`
}

// Sign computes the handshake signature for a Connect call.
func Sign(secret, agentID string, ts int64, nonce string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(agentID + "|" + strconv.FormatInt(ts, 10) + "|" + nonce))
	return hex.EncodeToString(m.Sum(nil))
}

// HubServer is implemented by the panel.
type HubServer interface {
	Register(context.Context, *RegisterRequest) (*RegisterResponse, error)
	Connect(grpc.ServerStream) error
}

func registerHandler(srv any, ctx context.Context, dec func(any) error, ic grpc.UnaryServerInterceptor) (any, error) {
	in := new(RegisterRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if ic == nil {
		return srv.(HubServer).Register(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: MethodRegister}
	return ic(ctx, in, info, func(ctx context.Context, req any) (any, error) {
		return srv.(HubServer).Register(ctx, req.(*RegisterRequest))
	})
}

func connectHandler(srv any, stream grpc.ServerStream) error {
	return srv.(HubServer).Connect(stream)
}

// ServiceDesc is registered on the panel's grpc.Server.
var ServiceDesc = grpc.ServiceDesc{
	ServiceName: ServiceName,
	HandlerType: (*HubServer)(nil),
	Methods:     []grpc.MethodDesc{{MethodName: "Register", Handler: registerHandler}},
	Streams: []grpc.StreamDesc{{
		StreamName:    "Connect",
		Handler:       connectHandler,
		ServerStreams: true,
		ClientStreams: true,
	}},
	Metadata: "proto/agenthub.proto",
}
