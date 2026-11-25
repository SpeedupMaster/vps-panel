package store

import "time"

// Admin is the single administrator account.
type Admin struct {
	ID           uint `gorm:"primaryKey"`
	Username     string
	PasswordHash string
	PasswordAt   time.Time
	TOTPSecret   string // encrypted; empty = 2FA off
	TOTPPending  string // encrypted secret awaiting confirmation
	RecoveryHash string // JSON array of bcrypt hashes
	LastTOTPStep int64  // last accepted 30s TOTP step (replay guard)
}

type Session struct {
	ID        string `gorm:"primaryKey"` // sha256 of the cookie value
	IP        string
	UA        string
	CreatedAt time.Time
	LastSeen  time.Time
	ExpiresAt time.Time
}

type Server struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	Name       string `gorm:"uniqueIndex" json:"name"`
	Group      string `json:"group"`
	Tags       string `json:"tags"`
	Sort       int    `json:"sort"`
	Mode       string `json:"mode"` // probe | node
	CapReality bool   `json:"cap_reality"`
	CapLanding bool   `json:"cap_landing"`

	AgentID          string    `gorm:"uniqueIndex" json:"agent_id"`
	AgentSecretEnc   string    `json:"-"`
	InstallTokenHash string    `json:"-"`
	InstallTokenExp  time.Time `json:"-"`

	IPv4        string `json:"ipv4"`
	IPv6        string `json:"ipv6"`
	Region      string `json:"region"`
	CountryCode string `json:"country_code"`

	// Asset info
	Provider    string     `json:"provider"`
	ProviderURL string     `json:"provider_url"`
	Price       float64    `json:"price"`
	Currency    string     `json:"currency"`
	PayCycle    string     `json:"pay_cycle"` // month | quarter | half | year | once
	ExpireAt    *time.Time `json:"expire_at"`
	Note        string     `json:"note"`
	// Port speed sold by the provider, Mbps; 0 = not set.
	BandwidthMbps int `json:"bandwidth_mbps"`

	// Traffic billing
	ResetDay   int    `json:"reset_day"`
	QuotaBytes int64  `json:"quota_bytes"`
	CountMode  string `json:"count_mode"`  // out | in | both | max
	OnExceeded string `json:"on_exceeded"` // alert | stop_xray

	// Reported by the agent
	AgentVersion      string    `json:"agent_version"`
	XrayVersion       string    `json:"xray_version"`
	XrayLockedVersion string    `json:"xray_locked_version"`
	XrayRunning       bool      `json:"xray_running"`
	XrayConfigVer     int       `json:"xray_config_ver"`
	XrayDrift         bool      `json:"xray_drift"`
	SystemJSON        string    `json:"-"`
	SwitchesJSON      string    `json:"-"`
	Status            string    `json:"status"`  // pending | online | suspect | offline
	Blocked           bool      `json:"blocked"` // suspected GFW block (backhaul probes)
	LastSeenAt        time.Time `json:"last_seen_at"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type RealityInbound struct {
	ServerID      uint   `gorm:"primaryKey" json:"server_id"`
	Port          int    `json:"port"`
	Target        string `json:"target"`       // host:port
	ServerNames   string `json:"server_names"` // comma separated
	PrivateKeyEnc string `json:"-"`
	PublicKey     string `json:"public_key"`
	ShortIDs      string `json:"short_ids"` // comma separated
	Address       string `json:"address"`   // client-facing host; empty = server IPv4
	Fingerprint   string `json:"fingerprint"`
}

type LandingInbound struct {
	ServerID uint   `gorm:"primaryKey" json:"server_id"`
	Port     int    `json:"port"`
	Method   string `json:"method"`
	// Address entries dial instead of the landing's public IP (internal
	// network, NAT port mapping). Empty = public IP.
	Address      string `json:"address"`
	ServerKeyEnc string `json:"-"`
}

type LandingUser struct {
	ID              uint `gorm:"primaryKey"`
	LandingServerID uint `gorm:"index"`
	EntryServerID   uint `gorm:"index"`
	UserKeyEnc      string
	Email           string
	CreatedAt       time.Time
}

type Route struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Name            string    `json:"name"`
	Type            string    `json:"type"` // direct | chain
	EntryServerID   uint      `gorm:"index" json:"entry_server_id"`
	LandingServerID uint      `json:"landing_server_id"`
	BackupIDs       string    `json:"backup_ids"` // comma separated server ids
	UUID            string    `json:"uuid"`
	Email           string    `gorm:"uniqueIndex" json:"email"`
	Enabled         bool      `json:"enabled"`
	InSub           bool      `json:"in_sub"`
	Sort            int       `json:"sort"`
	Tags            string    `json:"tags"`
	CreatedAt       time.Time `json:"created_at"`
}

type SubToken struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	Name          string     `json:"name"`
	TokenEnc      string     `json:"-"`
	TokenHash     string     `gorm:"uniqueIndex" json:"-"`
	TargetDefault string     `json:"target_default"`
	Routes        string     `json:"routes"` // comma separated route IDs; empty = follow global sub_routes
	LastUsedAt    *time.Time `json:"last_used_at"`
	LastIP        string     `json:"last_ip"`
	Enabled       bool       `json:"enabled"`
	CreatedAt     time.Time  `json:"created_at"`
}

type SubTemplate struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	Target  string `json:"target"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Active  bool   `json:"active"`
}

type SubAccessLog struct {
	ID       uint      `gorm:"primaryKey" json:"id"`
	TokenID  uint      `gorm:"index" json:"token_id"`
	IP       string    `json:"ip"`
	UA       string    `json:"ua"`
	Target   string    `json:"target"`
	Stranger bool      `json:"stranger"`
	TS       time.Time `gorm:"index" json:"ts"`
}

// Metric rows share one shape; Res picks the table via Scopes.
type Metric struct {
	ServerID  uint  `gorm:"primaryKey;autoIncrement:false"`
	TS        int64 `gorm:"primaryKey;autoIncrement:false"`
	CPU       float64
	Steal     float64
	Load1     float64
	MemUsed   uint64
	SwapUsed  uint64
	DiskUsed  uint64
	DiskRead  uint64
	DiskWrite uint64
	NetIn     uint64
	NetOut    uint64
	TCP       int
	UDP       int
}

type MetricRaw struct{ Metric }
type Metric1m struct{ Metric }
type Metric1h struct{ Metric }
type Metric1d struct{ Metric }

type ProbeTarget struct {
	ID       string `gorm:"primaryKey" json:"id"` // e.g. ct-bj
	Name     string `json:"name"`
	Carrier  string `json:"carrier"` // telecom | unicom | mobile
	Province string `json:"province"`
	City     string `json:"city"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Type     string `json:"type"`   // tcp | icmp
	Source   string `json:"source"` // zstatic | custom
	Enabled  bool   `json:"enabled"`
}

// ProbeResult rows: Kind backhaul (target = ProbeTarget.ID), segment (target =
// "seg-<entry>-<landing>"), peer (target = "peer-<server id>"), egress.
type ProbeResult struct {
	ServerID uint    `gorm:"primaryKey;autoIncrement:false" json:"server_id"`
	Target   string  `gorm:"primaryKey" json:"target"`
	TS       int64   `gorm:"primaryKey;autoIncrement:false" json:"ts"`
	Kind     string  `gorm:"index" json:"kind"`
	Avg      float64 `json:"avg"`
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Jitter   float64 `json:"jitter"`
	Loss     float64 `json:"loss"`
}

type ProbeResultHour struct{ ProbeResult }

type TrafficHourly struct {
	Scope   string `gorm:"primaryKey" json:"scope"` // server | route | landing_user | outbound
	ScopeID string `gorm:"primaryKey" json:"scope_id"`
	Hour    int64  `gorm:"primaryKey;autoIncrement:false" json:"hour"`
	Up      uint64 `json:"up"`
	Down    uint64 `json:"down"`
}

type TrafficSeq struct {
	AgentID string `gorm:"primaryKey"`
	LastSeq uint64
}

type ConfigVersion struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	ServerID  uint      `gorm:"index" json:"server_id"`
	Version   int       `json:"version"`
	Config    string    `json:"config"`
	Diff      string    `json:"diff"`
	Reason    string    `json:"reason"`
	Applied   bool      `json:"applied"`
	CreatedAt time.Time `json:"created_at"`
}

type Task struct {
	ID         uint64     `gorm:"primaryKey" json:"id,string"` // UnixNano: exceeds JS safe-integer range
	ServerID   uint       `gorm:"index" json:"server_id"`
	BatchID    string     `gorm:"index" json:"batch_id"`
	Type       string     `json:"type"`
	Args       string     `json:"args"`
	Payload    []byte     `json:"-"`
	Status     string     `json:"status"` // pending | sent | running | success | failed | timeout
	Output     string     `json:"output"`
	Data       string     `json:"data"`
	ExpireAt   *time.Time `json:"expire_at"`
	Timeout    int        `json:"timeout"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

type AlertRule struct {
	ID        uint    `gorm:"primaryKey" json:"id"`
	Name      string  `json:"name"`
	Kind      string  `json:"kind"` // offline, cpu, mem, disk, load, steal, backhaul_loss, backhaul_latency, blocked, segment_down, traffic, expire, new_version, stranger_sub, login
	Threshold float64 `json:"threshold"`
	Duration  int     `json:"duration"` // seconds the condition must hold
	Cooldown  int     `json:"cooldown"` // seconds between repeats
	Severity  string  `json:"severity"` // critical | warning | info
	Scope     string  `json:"scope"`    // all | node | entry | landing | probe | ids:1,2
	Channels  string  `json:"channels"` // comma separated channel ids, empty = all enabled
	Enabled   bool    `json:"enabled"`
}

type AlertEvent struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	RuleID     uint       `gorm:"index" json:"rule_id"`
	Kind       string     `json:"kind"`
	ServerID   uint       `gorm:"index" json:"server_id"`
	Key        string     `gorm:"index" json:"key"` // dedupe key
	Severity   string     `json:"severity"`
	Text       string     `json:"text"`
	Status     string     `json:"status"` // firing | resolved | notified
	Acked      bool       `json:"acked"`
	SilencedTo *time.Time `json:"silenced_to"`
	StartedAt  time.Time  `json:"started_at"`
	ResolvedAt *time.Time `json:"resolved_at"`
	LastSentAt *time.Time `json:"last_sent_at"`
}

type NotifyChannel struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Type      string `json:"type"` // telegram | bark | wecom | feishu | email | webhook
	Name      string `json:"name"`
	ConfigEnc string `json:"-"`
	MinLevel  string `json:"min_level"` // info | warning | critical
	Enabled   bool   `json:"enabled"`
}

type Release struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Component   string    `gorm:"index" json:"component"` // agent | xray | geo
	Version     string    `json:"version"`
	Channel     string    `json:"channel"` // stable | pre
	Assets      string    `json:"assets"`  // JSON: arch -> {url, sha256, sig}
	Notes       string    `json:"notes"`
	PublishedAt time.Time `json:"published_at"`
}

type AuditLog struct {
	ID     uint      `gorm:"primaryKey" json:"id"`
	Type   string    `gorm:"index" json:"type"` // login | server | route | sub | alert | system
	Action string    `json:"action"`
	Target string    `json:"target"`
	Detail string    `json:"detail"`
	IP     string    `json:"ip"`
	TS     time.Time `gorm:"index" json:"ts"`
}

type Setting struct {
	Key   string `gorm:"primaryKey"`
	Value string
}

type ToolResult struct {
	ID       uint      `gorm:"primaryKey" json:"id"`
	ServerID uint      `gorm:"index" json:"server_id"`
	Tool     string    `gorm:"index" json:"tool"`
	Summary  string    `json:"summary"`
	Data     string    `json:"data"`
	TS       time.Time `json:"ts"`
}

type Script struct {
	ID      uint   `gorm:"primaryKey" json:"id"`
	Name    string `json:"name"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

type Schedule struct {
	ID      uint      `gorm:"primaryKey" json:"id"`
	Name    string    `json:"name"`
	Cron    string    `json:"cron"`    // "HH:MM" daily or "@every 6h"
	Action  string    `json:"action"`  // restart_xray | update_geo | unlock_check | ...
	Servers string    `json:"servers"` // comma separated ids or "all"
	Enabled bool      `json:"enabled"`
	LastRun time.Time `json:"last_run"`
}

func allModels() []any {
	return []any{
		&Admin{}, &Session{}, &Server{}, &RealityInbound{}, &LandingInbound{}, &LandingUser{},
		&Route{}, &SubToken{}, &SubTemplate{}, &SubAccessLog{},
		&MetricRaw{}, &Metric1m{}, &Metric1h{}, &Metric1d{},
		&ProbeTarget{}, &ProbeResult{}, &ProbeResultHour{}, &TrafficHourly{}, &TrafficSeq{},
		&ConfigVersion{}, &Task{}, &AlertRule{}, &AlertEvent{}, &NotifyChannel{}, &Release{},
		&AuditLog{}, &Setting{}, &ToolResult{}, &Script{}, &Schedule{},
	}
}

func (MetricRaw) TableName() string       { return "metrics_raw" }
func (Metric1m) TableName() string        { return "metrics_1m" }
func (Metric1h) TableName() string        { return "metrics_1h" }
func (Metric1d) TableName() string        { return "metrics_1d" }
func (ProbeResult) TableName() string     { return "probe_results" }
func (ProbeResultHour) TableName() string { return "probe_results_1h" }
func (TrafficHourly) TableName() string   { return "traffic_hourly" }
