package proto

import (
	"reflect"
	"testing"

	"google.golang.org/grpc/encoding"
)

func TestSignDeterministicAndSensitive(t *testing.T) {
	s1 := Sign("secret", "agent-1", 1700000000, "nonce-a")
	s2 := Sign("secret", "agent-1", 1700000000, "nonce-a")
	if s1 != s2 || len(s1) != 64 {
		t.Fatalf("Sign mismatch or wrong len: %q vs %q", s1, s2)
	}
	if s1 == Sign("other", "agent-1", 1700000000, "nonce-a") {
		t.Fatal("Sign did not vary with secret")
	}
	if s1 == Sign("secret", "agent-2", 1700000000, "nonce-a") {
		t.Fatal("Sign did not vary with agentID")
	}
	if s1 == Sign("secret", "agent-1", 1700000001, "nonce-a") {
		t.Fatal("Sign did not vary with timestamp")
	}
	if s1 == Sign("secret", "agent-1", 1700000000, "nonce-b") {
		t.Fatal("Sign did not vary with nonce")
	}
}

func TestJSONCodecV2RoundTrip(t *testing.T) {
	codec := encoding.GetCodecV2(CodecName)
	if codec == nil {
		t.Fatalf("CodecV2 %q not registered", CodecName)
	}
	if codec.Name() != CodecName {
		t.Fatalf("codec.Name() = %q, want %q", codec.Name(), CodecName)
	}

	wantAgent := &AgentMessage{
		Traffic: &TrafficDelta{
			Seq: 42,
			TS:  1700000042,
			Items: []TrafficItem{
				{Scope: "user", Name: "r-1", Up: 1234, Down: 5678},
			},
		},
	}
	bufs, err := codec.Marshal(wantAgent)
	if err != nil {
		t.Fatalf("Marshal AgentMessage: %v", err)
	}
	defer bufs.Free()

	var gotAgent AgentMessage
	if err := codec.Unmarshal(bufs, &gotAgent); err != nil {
		t.Fatalf("Unmarshal AgentMessage: %v", err)
	}
	if !reflect.DeepEqual(&gotAgent, wantAgent) {
		t.Fatalf("AgentMessage round-trip = %+v, want %+v", &gotAgent, wantAgent)
	}

	wantPanel := &PanelMessage{
		Ack: &Ack{Seq: 42},
	}
	pbufs, err := codec.Marshal(wantPanel)
	if err != nil {
		t.Fatalf("Marshal PanelMessage: %v", err)
	}
	defer pbufs.Free()

	var gotPanel PanelMessage
	if err := codec.Unmarshal(pbufs, &gotPanel); err != nil {
		t.Fatalf("Unmarshal PanelMessage: %v", err)
	}
	if !reflect.DeepEqual(&gotPanel, wantPanel) {
		t.Fatalf("PanelMessage round-trip = %+v, want %+v", &gotPanel, wantPanel)
	}
}

func TestAgentMessageValidate(t *testing.T) {
	var nilMsg *AgentMessage
	if err := nilMsg.Validate(); err == nil {
		t.Fatal("expected nil AgentMessage to fail Validate")
	}
	if err := (&AgentMessage{}).Validate(); err == nil {
		t.Fatal("expected empty AgentMessage to fail Validate")
	}
	if err := (&AgentMessage{Hello: &Hello{}, Metrics: &Metrics{}}).Validate(); err == nil {
		t.Fatal("expected multi-field AgentMessage to fail Validate")
	}

	valid := []*AgentMessage{
		{Hello: &Hello{}},
		{Metrics: &Metrics{}},
		{Traffic: &TrafficDelta{}},
		{Probe: &ProbeResult{}},
		{TaskResult: &TaskResult{}},
		{XrayState: &XrayState{}},
		{Event: &Event{}},
	}
	for i, m := range valid {
		if err := m.Validate(); err != nil {
			t.Errorf("valid AgentMessage[%d] failed Validate: %v", i, err)
		}
	}
}

func TestPanelMessageValidate(t *testing.T) {
	var nilMsg *PanelMessage
	if err := nilMsg.Validate(); err == nil {
		t.Fatal("expected nil PanelMessage to fail Validate")
	}
	if err := (&PanelMessage{}).Validate(); err == nil {
		t.Fatal("expected empty PanelMessage to fail Validate")
	}
	if err := (&PanelMessage{Ack: &Ack{}, Task: &Task{}}).Validate(); err == nil {
		t.Fatal("expected multi-field PanelMessage to fail Validate")
	}

	valid := []*PanelMessage{
		{ApplyConfig: &ApplyConfig{}},
		{XrayOp: &XrayOp{}},
		{ProbeTasks: &ProbeTasks{}},
		{Task: &Task{}},
		{Ack: &Ack{}},
		{Settings: &AgentSettings{}},
		{ShellInput: &ShellInput{}},
	}
	for i, m := range valid {
		if err := m.Validate(); err != nil {
			t.Errorf("valid PanelMessage[%d] failed Validate: %v", i, err)
		}
	}
}
