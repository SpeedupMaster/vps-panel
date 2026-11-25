// Package proto defines the messages exchanged between the panel and agents.
//
// The transport is a gRPC bidirectional stream (see service.go). Messages are
// plain Go structs encoded with a JSON codec, so no protoc step is needed.
// proto/agenthub.proto documents the same shapes for reference.
package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ---------- Agent -> Panel ----------

// AgentMessage is one frame sent by an agent. Exactly one field is set.
type AgentMessage struct {
	Hello      *Hello        `json:"hello,omitempty"`
	Metrics    *Metrics      `json:"metrics,omitempty"`
	Traffic    *TrafficDelta `json:"traffic,omitempty"`
	Probe      *ProbeResult  `json:"probe,omitempty"`
	TaskResult *TaskResult   `json:"task_result,omitempty"`
	XrayState  *XrayState    `json:"xray_state,omitempty"`
	Event      *Event        `json:"event,omitempty"`
}

// Validate enforces the oneof invariant: exactly one payload field must be set.
func (m *AgentMessage) Validate() error {
	if m == nil {
		return errors.New("nil AgentMessage")
	}
	n := 0
	if m.Hello != nil {
		n++
	}
	if m.Metrics != nil {
		n++
	}
	if m.Traffic != nil {
		n++
	}
	if m.Probe != nil {
		n++
	}
	if m.TaskResult != nil {
		n++
	}
	if m.XrayState != nil {
		n++
	}
	if m.Event != nil {
		n++
	}
	if n != 1 {
		return fmt.Errorf("AgentMessage must have exactly 1 payload field set, got %d", n)
	}
	return nil
}

// Hello is the first frame after the stream opens.
type Hello struct {
	Version  string        `json:"version"`
	Mode     string        `json:"mode"` // probe | node
	Switches LocalSwitches `json:"switches"`
	System   SystemInfo    `json:"system"`
}

// LocalSwitches can only be changed in /etc/vpsagent/agent.yaml on the VPS.
type LocalSwitches struct {
	AllowRemoteExec bool `json:"allow_remote_exec"`
	AllowSystemTune bool `json:"allow_system_tune"`
	AllowSelfUpdate bool `json:"allow_self_update"`
}

type SystemInfo struct {
	Hostname   string `json:"hostname"`
	OS         string `json:"os"`
	Kernel     string `json:"kernel"`
	Arch       string `json:"arch"`
	Virt       string `json:"virt"` // kvm, openvz, lxc, ...
	CPUModel   string `json:"cpu_model"`
	CPUCores   int    `json:"cpu_cores"`
	MemTotal   uint64 `json:"mem_total"`
	SwapTotal  uint64 `json:"swap_total"`
	DiskTotal  uint64 `json:"disk_total"`
	BootTime   int64  `json:"boot_time"`
	IPv4       string `json:"ipv4"`
	IPv6       string `json:"ipv6"`
	Congestion string `json:"congestion"` // current tcp_congestion_control
	Qdisc      string `json:"qdisc"`
	AvailCC    string `json:"avail_cc"`
}

type Metrics struct {
	TS        int64     `json:"ts"`
	CPU       float64   `json:"cpu"`   // percent
	Steal     float64   `json:"steal"` // percent
	Load1     float64   `json:"load1"`
	Load5     float64   `json:"load5"`
	Load15    float64   `json:"load15"`
	MemUsed   uint64    `json:"mem_used"`
	SwapUsed  uint64    `json:"swap_used"`
	DiskUsed  uint64    `json:"disk_used"`
	DiskRead  uint64    `json:"disk_read"`  // bytes/s
	DiskWrite uint64    `json:"disk_write"` // bytes/s
	NetIn     uint64    `json:"net_in"`     // bytes/s
	NetOut    uint64    `json:"net_out"`    // bytes/s
	TCPConns  int       `json:"tcp"`
	UDPConns  int       `json:"udp"`
	Uptime    int64     `json:"uptime"`
	Xray      *XrayProc `json:"xray,omitempty"`
}

type XrayProc struct {
	Running bool    `json:"running"`
	PID     int     `json:"pid"`
	CPU     float64 `json:"cpu"`
	Mem     uint64  `json:"mem"`
	Uptime  int64   `json:"uptime"`
	Version string  `json:"version"`
}

// TrafficDelta carries byte deltas since the previous report. Seq is strictly
// increasing per agent; the panel ignores frames with seq <= last seen.
type TrafficDelta struct {
	Seq   uint64        `json:"seq"`
	TS    int64         `json:"ts"`
	Items []TrafficItem `json:"items"`
}

type TrafficItem struct {
	Scope string `json:"scope"` // server | user | outbound | inbound
	Name  string `json:"name"`  // iface / email / outbound tag
	Up    uint64 `json:"up"`
	Down  uint64 `json:"down"`
}

type ProbeResult struct {
	TS      int64         `json:"ts"`
	Kind    string        `json:"kind"` // backhaul | segment | peer | egress
	Samples []ProbeSample `json:"samples"`
}

type ProbeSample struct {
	Target string  `json:"target"` // target id / route email / server id
	Avg    float64 `json:"avg"`    // ms, -1 when all lost
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Jitter float64 `json:"jitter"`
	Loss   float64 `json:"loss"` // 0..1 from agents; the panel converts to percent on ingest
}

type TaskResult struct {
	TaskID   uint64 `json:"task_id"`
	Status   string `json:"status"` // running | success | failed
	Output   string `json:"output"` // appended chunk when Status == running
	Data     any    `json:"data,omitempty"`
	Finished bool   `json:"finished"`
}

type XrayState struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version"`
	Running    bool   `json:"running"`
	ConfigHash string `json:"config_hash"`
	ConfigVer  int    `json:"config_ver"`
	Drift      bool   `json:"drift"` // local file differs from the last applied config
}

type Event struct {
	TS     int64  `json:"ts"`
	Kind   string `json:"kind"` // xray_crash, xray_restart, config_drift, ip_changed, rollback
	Detail string `json:"detail"`
}

// ---------- Panel -> Agent ----------

type PanelMessage struct {
	ApplyConfig *ApplyConfig   `json:"apply_config,omitempty"`
	XrayOp      *XrayOp        `json:"xray_op,omitempty"`
	ProbeTasks  *ProbeTasks    `json:"probe_tasks,omitempty"`
	Task        *Task          `json:"task,omitempty"`
	Ack         *Ack           `json:"ack,omitempty"`
	Settings    *AgentSettings `json:"settings,omitempty"`
	ShellInput  *ShellInput    `json:"shell_input,omitempty"`
}

// Validate enforces the oneof invariant: exactly one payload field must be set.
func (m *PanelMessage) Validate() error {
	if m == nil {
		return errors.New("nil PanelMessage")
	}
	n := 0
	if m.ApplyConfig != nil {
		n++
	}
	if m.XrayOp != nil {
		n++
	}
	if m.ProbeTasks != nil {
		n++
	}
	if m.Task != nil {
		n++
	}
	if m.Ack != nil {
		n++
	}
	if m.Settings != nil {
		n++
	}
	if m.ShellInput != nil {
		n++
	}
	if n != 1 {
		return fmt.Errorf("PanelMessage must have exactly 1 payload field set, got %d", n)
	}
	return nil
}

// ShellInput feeds keystrokes / resize events into a WebSSH session.
type ShellInput struct {
	TaskID uint64 `json:"task_id"`
	Data   []byte `json:"data,omitempty"`
	Cols   int    `json:"cols,omitempty"`
	Rows   int    `json:"rows,omitempty"`
	Close  bool   `json:"close,omitempty"`
}

// ApplyConfig replaces the whole Xray config. The agent tests it with
// `xray run -test` before swapping and reports the outcome as a TaskResult.
type ApplyConfig struct {
	TaskID   uint64          `json:"task_id"`
	Version  int             `json:"version"`
	Config   []byte          `json:"config"`
	Firewall *FirewallPolicy `json:"firewall,omitempty"`
	Restart  bool            `json:"restart"`
}

// FirewallPolicy restricts the SS landing port to the entry machines' IPs.
type FirewallPolicy struct {
	Port    int      `json:"port"`
	Allowed []string `json:"allowed"`
}

// XrayOp hot-adds or removes users / routing rules through the Xray API.
type XrayOp struct {
	TaskID uint64    `json:"task_id"`
	Op     string    `json:"op"` // add_user | remove_user
	Tag    string    `json:"tag"`
	User   *XrayUser `json:"user,omitempty"`
	// Rule is the routing rule (with ruleTag = user email) added alongside.
	Rule json.RawMessage `json:"rule,omitempty"`
	// FullConfig lets the agent fall back to a restart if the API call fails.
	FullConfig []byte `json:"full_config,omitempty"`
	Version    int    `json:"version"`
}

type XrayUser struct {
	Email    string `json:"email"`
	ID       string `json:"id,omitempty"`
	Flow     string `json:"flow,omitempty"`
	Password string `json:"password,omitempty"`
}

type ProbeTasks struct {
	IntervalSec int           `json:"interval_sec"`
	Count       int           `json:"count"` // probes per round
	Targets     []ProbeTarget `json:"targets"`
}

type ProbeTarget struct {
	ID   string `json:"id"`
	Kind string `json:"kind"` // backhaul | segment | peer | egress
	Type string `json:"type"` // tcp | icmp | http
	Host string `json:"host"`
	Port int    `json:"port"`
	URL  string `json:"url,omitempty"`
}

// Task is a one-off job; Type picks the handler on the agent.
type Task struct {
	ID      uint64            `json:"id"`
	Type    string            `json:"type"`
	Args    map[string]string `json:"args,omitempty"`
	Payload []byte            `json:"payload,omitempty"`
	Timeout int               `json:"timeout"` // seconds
}

// IPQualityKeys is the ip_quality task payload: optional third-party API keys.
type IPQualityKeys struct {
	IPAPIIs string `json:"ipapi_is,omitempty"`
}

type Ack struct {
	Seq uint64 `json:"seq"`
}

type AgentSettings struct {
	ReportInterval int      `json:"report_interval"`      // seconds
	PanelURLs      []string `json:"panel_urls,omitempty"` // primary first; agent reconnects in order
}

// Task types understood by the agent.
const (
	TaskInstallXray   = "install_xray"
	TaskUninstallXray = "uninstall_xray"
	TaskUpdateXray    = "update_xray"
	TaskUpdateAgent   = "update_agent"
	TaskUpdateGeo     = "update_geo"
	TaskRestartXray   = "restart_xray"
	TaskRestartAgent  = "restart_agent"
	TaskXrayLog       = "xray_log"
	TaskTuneNet       = "tune_net"
	TaskRestoreNet    = "restore_net"
	TaskNetStatus     = "net_status"
	TaskRealityScan   = "reality_scan"
	TaskDiagnose      = "diagnose"
	TaskUnlock        = "unlock_check"
	TaskIPQuality     = "ip_quality"
	TaskTrace         = "trace"
	TaskSpeedtest     = "speedtest"
	TaskIperf         = "iperf"
	TaskExec          = "exec"
	TaskShell         = "shell"
)

// Now returns the current unix time; centralized for tests.
func Now() int64 { return time.Now().Unix() }
