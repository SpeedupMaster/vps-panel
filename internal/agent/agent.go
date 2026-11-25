// Package agent is the VPS-side daemon: it dials the panel, reports
// metrics / traffic / probes and executes tasks.
package agent

import (
	"context"
	crand "crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"

	"vpspanel/internal/agent/buffer"
	"vpspanel/internal/agent/collector"
	"vpspanel/internal/agent/config"
	"vpspanel/internal/agent/prober"
	"vpspanel/internal/common/proto"
	"vpspanel/internal/common/version"
)

// Xray is implemented by xraymgr (node mode only).
type Xray interface {
	Proc() *proto.XrayProc
	State() *proto.XrayState
	Stats() []proto.TrafficItem
	Apply(ctx context.Context, a *proto.ApplyConfig, out func(string)) error
	Op(ctx context.Context, op *proto.XrayOp, out func(string)) error
	Watch(ctx context.Context, events chan<- *proto.Event)
}

type TaskFunc func(ctx context.Context, t *proto.Task, out func(string)) (any, error)

type Agent struct {
	Creds *config.Credentials
	Local config.Local
	Col   *collector.Collector
	Buf   *buffer.Buffer
	Xray  Xray // nil in probe mode
	Tasks map[string]TaskFunc
	// OnConnected runs after the first panel message of each session.
	OnConnected func()

	mu          sync.Mutex
	send        chan *proto.AgentMessage
	trafficWake chan struct{}
	interval    time.Duration
	probes      *proto.ProbeTasks
	probeReset  chan struct{}
	shells      map[uint64]chan *proto.ShellInput
}

const (
	trafficBatchSize = 200
	xrayOpTimeout    = 90 * time.Second
)

var (
	ipRefreshInterval = 20 * time.Minute
	errIPChanged      = errors.New("public IP changed")
)

// PanelURLs returns a concurrency-safe snapshot of configured panel URLs.
func (a *Agent) PanelURLs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.Creds == nil {
		return nil
	}
	return append([]string(nil), a.Creds.PanelURLs...)
}

func randomNonce() (string, error) {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func New(creds *config.Credentials, local config.Local, buf *buffer.Buffer) *Agent {
	return &Agent{
		Creds: creds, Local: local, Col: collector.New(), Buf: buf,
		Tasks: map[string]TaskFunc{}, interval: 10 * time.Second,
		probeReset: make(chan struct{}, 1), shells: map[uint64]chan *proto.ShellInput{},
	}
}

// Register exchanges an install token for credentials.
func Register(panelURL, token, mode string) (*config.Credentials, error) {
	conn, err := dial(panelURL)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	col := collector.New()
	sys := col.System()
	resp := new(proto.RegisterResponse)
	err = conn.Invoke(ctx, proto.MethodRegister, &proto.RegisterRequest{Token: token, Mode: mode, Hostname: sys.Hostname, System: sys}, resp, proto.CallOption())
	if err != nil {
		return nil, err
	}
	return &config.Credentials{PanelURLs: []string{strings.TrimRight(panelURL, "/")}, AgentID: resp.AgentID, AgentSecret: resp.AgentSecret, ServerName: resp.ServerName}, nil
}

func dial(panelURL string) (*grpc.ClientConn, error) {
	u, err := url.Parse(panelURL)
	if err != nil {
		return nil, err
	}
	host := u.Host
	var creds credentials.TransportCredentials
	if u.Scheme == "https" {
		if u.Port() == "" {
			host = net.JoinHostPort(u.Hostname(), "443")
		}
		creds = credentials.NewTLS(&tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12})
	} else {
		if u.Port() == "" {
			host = net.JoinHostPort(u.Hostname(), "80")
		}
		creds = insecure.NewCredentials()
	}
	return grpc.NewClient(host,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(proto.CallOption(), grpc.MaxCallRecvMsgSize(8<<20)),
		grpc.WithReadBufferSize(4<<10),
		grpc.WithWriteBufferSize(4<<10),
		grpc.WithSharedWriteBuffer(true),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 10 * time.Second}),
	)
}

// Run keeps a connection to the panel, trying each panel URL in order with
// exponential backoff (1s .. 60s, jittered).
func (a *Agent) Run(ctx context.Context) {
	if a.Xray != nil {
		go a.watchXray(ctx)
	}
	go a.trafficLoop(ctx)
	backoff := time.Second
	idx := 0
	tried := 0
	for ctx.Err() == nil {
		urls := a.PanelURLs()
		if len(urls) == 0 {
			log.Print("no panel URL configured")
			return
		}
		u := urls[idx%len(urls)]
		start := time.Now()
		err := a.session(ctx, u)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errIPChanged) {
			backoff = time.Second
			tried = 0
			continue
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second // the session was healthy
			tried = 0
		} else if len(urls) > 1 {
			idx++ // try the next panel address
			tried++
			if tried < len(urls) {
				backoff = time.Second
				log.Printf("panel %s: %v; switching to next panel URL", u, err)
				continue
			}
			tried = 0
		}
		wait := backoff + time.Duration(rand.Int63n(int64(backoff/2)+1))
		log.Printf("panel %s: %v; retry in %s", u, err, wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, 60*time.Second)
	}
}

func (a *Agent) session(rootCtx context.Context, panelURL string) error {
	conn, err := dial(panelURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(rootCtx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		_ = conn.Close()
		wg.Wait()
	}()

	ts := time.Now().Unix()
	nonce, err := randomNonce()
	if err != nil {
		return err
	}
	md := metadata.Pairs(proto.MDAgentID, a.Creds.AgentID, proto.MDTime, strconv.FormatInt(ts, 10),
		proto.MDNonce, nonce, proto.MDSig, proto.Sign(a.Creds.AgentSecret, a.Creds.AgentID, ts, nonce))
	stream, err := conn.NewStream(metadata.NewOutgoingContext(ctx, md), &proto.ServiceDesc.Streams[0], proto.MethodConnect)
	if err != nil {
		return err
	}

	send := make(chan *proto.AgentMessage, 512)
	wake := make(chan struct{}, 1)
	a.mu.Lock()
	a.send = send
	a.trafficWake = wake
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.send == send {
			a.send = nil
			a.trafficWake = nil
		}
		a.mu.Unlock()
	}()

	errc := make(chan error, 4)
	wg.Add(1)
	go func() { // writer
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case m := <-send:
				if err := stream.SendMsg(m); err != nil {
					errc <- err
					return
				}
			}
		}
	}()

	sys := a.Col.System()
	hello := &proto.Hello{Version: version.Version, Mode: a.Local.Mode, Switches: a.Local.Switches, System: sys}
	send <- &proto.AgentMessage{Hello: hello}
	if a.Xray != nil {
		send <- &proto.AgentMessage{XrayState: a.Xray.State()}
	}
	wg.Add(4)
	go func() {
		defer wg.Done()
		a.drainTraffic(ctx, send, wake)
	}()
	log.Printf("connected to %s as %s", panelURL, a.Creds.ServerName)

	go func() {
		defer wg.Done()
		a.metricsLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		a.probeLoop(ctx)
	}()
	go func() {
		defer wg.Done()
		a.ipRefreshLoop(ctx, sys.IPv4, sys.IPv6, errc)
	}()
	wg.Add(1)
	go func() { // reader
		defer wg.Done()
		first := true
		for {
			m := new(proto.PanelMessage)
			if err := stream.RecvMsg(m); err != nil {
				errc <- err
				return
			}
			if first && a.OnConnected != nil {
				first = false
				go a.OnConnected()
			}
			a.handle(rootCtx, m, panelURL)
		}
	}()
	return <-errc
}

// Emit queues a message; it is dropped when disconnected (metrics) — traffic
// survives via the buffer.
func (a *Agent) Emit(m *proto.AgentMessage) bool {
	a.mu.Lock()
	ch := a.send
	a.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- m:
		return true
	default:
		return false
	}
}

func (a *Agent) handle(ctx context.Context, m *proto.PanelMessage, current string) {
	switch {
	case m.Ack != nil:
		if a.Buf != nil {
			a.Buf.Ack(m.Ack.Seq)
		}
		a.notifyTraffic()
	case m.Settings != nil:
		var saveCopy *config.Credentials
		a.mu.Lock()
		if m.Settings.ReportInterval >= 2 {
			a.interval = time.Duration(m.Settings.ReportInterval) * time.Second
		}
		if a.Creds != nil {
			if urls := mergePanelURLs(m.Settings.PanelURLs, current); !slices.Equal(urls, a.Creds.PanelURLs) {
				a.Creds.PanelURLs = urls
				cp := *a.Creds
				cp.PanelURLs = append([]string(nil), urls...)
				saveCopy = &cp
			}
		}
		a.mu.Unlock()
		if saveCopy != nil {
			if err := config.SaveCredentials(saveCopy); err != nil {
				log.Printf("save panel urls: %v", err)
			}
		}
	case m.ProbeTasks != nil:
		a.mu.Lock()
		a.probes = m.ProbeTasks
		a.mu.Unlock()
		select {
		case a.probeReset <- struct{}{}:
		default:
		}
	case m.ApplyConfig != nil:
		go a.runApply(ctx, m.ApplyConfig)
	case m.XrayOp != nil:
		go a.runXrayOp(ctx, m.XrayOp)
	case m.Task != nil:
		go a.runTask(ctx, m.Task)
	case m.ShellInput != nil:
		a.mu.Lock()
		ch := a.shells[m.ShellInput.TaskID]
		a.mu.Unlock()
		if ch != nil {
			select {
			case ch <- m.ShellInput:
			default:
			}
		}
	}
}

func (a *Agent) metricsLoop(ctx context.Context) {
	for {
		m := a.Col.Sample()
		if a.Xray != nil {
			m.Xray = a.Xray.Proc()
		}
		a.Emit(&proto.AgentMessage{Metrics: m})
		a.mu.Lock()
		iv := a.interval
		a.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
		}
	}
}

// trafficLoop turns NIC counters and Xray stats into deltas once a minute.
// It runs for the whole process lifetime so deltas accumulate offline.
func (a *Agent) trafficLoop(ctx context.Context) {
	var last collector.NetCounters
	if !a.Buf.Get("nic", &last) {
		last = a.Col.ReadNet()
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cur := a.Col.ReadNet()
		var up, down uint64
		// Counter reset (reboot / iface change): take the current value as the delta.
		if cur.TX >= last.TX {
			up = cur.TX - last.TX
		} else {
			up = cur.TX
		}
		if cur.RX >= last.RX {
			down = cur.RX - last.RX
		} else {
			down = cur.RX
		}
		last = cur
		items := []proto.TrafficItem{{Scope: "server", Name: "nic", Up: up, Down: down}}
		if a.Xray != nil {
			items = append(items, a.Xray.Stats()...)
		}
		if _, err := a.Buf.AddWithMeta(time.Now().Unix(), items, "nic", last); err != nil {
			log.Printf("buffer: %v", err)
			continue
		}
		a.notifyTraffic()
	}
}

func (a *Agent) notifyTraffic() {
	a.mu.Lock()
	ch := a.trafficWake
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// drainTraffic sends buffered traffic deltas strictly in sequence order in
// bounded batches, waiting for each batch to be acknowledged before sending
// newer deltas so a later Ack can never wipe older unacked items.
func (a *Agent) drainTraffic(ctx context.Context, send chan<- *proto.AgentMessage, wake <-chan struct{}) {
	if a.Buf == nil {
		return
	}
	for {
		batch := a.Buf.Pending(trafficBatchSize)
		if len(batch) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				continue
			}
		}
		for _, d := range batch {
			select {
			case send <- &proto.AgentMessage{Traffic: d}:
			case <-ctx.Done():
				return
			}
		}
		target := batch[len(batch)-1].Seq
		for {
			first := a.Buf.Pending(1)
			if len(first) == 0 || first[0].Seq > target {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
		}
	}
}

func nextProbeWait(interval, elapsed time.Duration) time.Duration {
	const minWait = 5 * time.Second
	if interval < minWait {
		return minWait
	}
	rem := interval - elapsed
	if rem < minWait {
		return minWait
	}
	return rem
}

func (a *Agent) probeLoop(ctx context.Context) {
	for {
		a.mu.Lock()
		pt := a.probes
		a.mu.Unlock()
		wait := time.Minute
		start := time.Now()
		ran := false
		if pt != nil && len(pt.Targets) > 0 {
			ran = true
			if pt.IntervalSec >= 10 {
				wait = time.Duration(pt.IntervalSec) * time.Second
			}
			res := prober.MeasureAllContext(ctx, pt.Targets, pt.Count, 3*time.Second)
			if ctx.Err() != nil {
				return
			}
			now := time.Now().Unix()
			for kind, samples := range res {
				a.Emit(&proto.AgentMessage{Probe: &proto.ProbeResult{TS: now, Kind: kind, Samples: samples}})
			}
		}
		if ran {
			wait = nextProbeWait(wait, time.Since(start))
		}
		select {
		case <-ctx.Done():
			return
		case <-a.probeReset:
		case <-time.After(wait):
		}
	}
}

// ipRefreshLoop periodically re-checks public IPs so dynamic IP changes on a
// long-lived stream trigger a panel update and clean reconnect.
func (a *Agent) ipRefreshLoop(ctx context.Context, lastV4, lastV6 string, errc chan<- error) {
	iv := ipRefreshInterval
	if iv <= 0 {
		return
	}
	t := time.NewTicker(iv)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		curV4, curV6 := a.Col.CurrentIPs()
		v4Changed := curV4 != "" && curV4 != lastV4
		v6Changed := curV6 != "" && curV6 != lastV6
		if !v4Changed && !v6Changed {
			continue
		}
		log.Printf("public IP changed (%s/%s -> %s/%s); reconnecting to update panel", lastV4, lastV6, curV4, curV6)
		sys := a.Col.System()
		if curV4 != "" {
			sys.IPv4 = curV4
		}
		if curV6 != "" {
			sys.IPv6 = curV6
		}
		a.Emit(&proto.AgentMessage{Hello: &proto.Hello{
			Version: version.Version, Mode: a.Local.Mode, Switches: a.Local.Switches, System: sys,
		}})
		select {
		case errc <- errIPChanged:
		default:
		}
		return
	}
}

func (a *Agent) watchXray(ctx context.Context) {
	events := make(chan *proto.Event, 16)
	go a.Xray.Watch(ctx, events)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-events:
			a.Emit(&proto.AgentMessage{Event: e})
			a.Emit(&proto.AgentMessage{XrayState: a.Xray.State()})
		case <-t.C:
			a.Emit(&proto.AgentMessage{XrayState: a.Xray.State()})
		}
	}
}

// ---------- tasks ----------

// output batches streamed output to the panel.
type output struct {
	a         *Agent
	id        uint64
	immediate bool // interactive shells flush every write
	mu        sync.Mutex
	buf       strings.Builder
	last      time.Time
}

func (o *output) write(s string) {
	o.mu.Lock()
	o.buf.WriteString(s)
	flush := o.immediate || time.Since(o.last) > 500*time.Millisecond || o.buf.Len() > 16<<10
	var chunk string
	if flush {
		chunk = o.buf.String()
		o.buf.Reset()
		o.last = time.Now()
	}
	o.mu.Unlock()
	if chunk != "" {
		o.a.Emit(&proto.AgentMessage{TaskResult: &proto.TaskResult{TaskID: o.id, Status: "running", Output: chunk}})
	}
}

func (o *output) rest() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := o.buf.String()
	o.buf.Reset()
	return s
}

func (a *Agent) finish(id uint64, out *output, data any, err error) {
	r := &proto.TaskResult{TaskID: id, Status: "success", Data: data, Finished: true, Output: out.rest()}
	if err != nil {
		r.Status = "failed"
		r.Output += "\n错误：" + err.Error()
	}
	// Final results matter: retry briefly if the stream is reconnecting.
	for i := 0; i < 30 && !a.Emit(&proto.AgentMessage{TaskResult: r}); i++ {
		time.Sleep(2 * time.Second)
	}
}

func (a *Agent) runTask(rootCtx context.Context, t *proto.Task) {
	out := &output{a: a, id: t.ID, last: time.Now(), immediate: t.Type == proto.TaskShell}
	fn := a.Tasks[t.Type]
	if fn == nil {
		a.finish(t.ID, out, nil, fmt.Errorf("Agent 不支持任务类型 %s（可能需要更新 Agent）", t.Type))
		return
	}
	timeout := time.Duration(t.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(rootCtx, timeout)
	defer cancel()
	a.Emit(&proto.AgentMessage{TaskResult: &proto.TaskResult{TaskID: t.ID, Status: "running"}})
	data, err := fn(ctx, t, out.write)
	a.finish(t.ID, out, data, err)
}

func (a *Agent) runApply(rootCtx context.Context, ac *proto.ApplyConfig) {
	out := &output{a: a, id: ac.TaskID, last: time.Now()}
	if a.Xray == nil {
		a.finish(ac.TaskID, out, nil, errors.New("这台机器是仅探针模式"))
		return
	}
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	// Decouple from the transient stream context so a brief reconnect mid-Apply
	// cannot cancel waitActive or break rollback RestartService.
	ctx, cancel := context.WithTimeout(rootCtx, xrayOpTimeout)
	defer cancel()
	err := a.Xray.Apply(ctx, ac, out.write)
	a.finish(ac.TaskID, out, a.Xray.State(), err)
	a.Emit(&proto.AgentMessage{XrayState: a.Xray.State()})
}

func (a *Agent) runXrayOp(rootCtx context.Context, op *proto.XrayOp) {
	out := &output{a: a, id: op.TaskID, last: time.Now()}
	if a.Xray == nil {
		a.finish(op.TaskID, out, nil, errors.New("这台机器是仅探针模式"))
		return
	}
	if rootCtx == nil {
		rootCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(rootCtx, xrayOpTimeout)
	defer cancel()
	err := a.Xray.Op(ctx, op, out.write)
	a.finish(op.TaskID, out, a.Xray.State(), err)
	a.Emit(&proto.AgentMessage{XrayState: a.Xray.State()})
}

// RegisterShell lets a WebSSH task receive input frames.
func (a *Agent) RegisterShell(id uint64) chan *proto.ShellInput {
	ch := make(chan *proto.ShellInput, 256)
	a.mu.Lock()
	a.shells[id] = ch
	a.mu.Unlock()
	return ch
}

func (a *Agent) UnregisterShell(id uint64) {
	a.mu.Lock()
	delete(a.shells, id)
	a.mu.Unlock()
}

// mergePanelURLs takes the panel's address list but keeps the one this agent
// is connected through as the last fallback: the configured address may not
// be reachable from here (LAN-only, or still being set up), and dropping the
// working one would lock the agent out after its next restart.
func mergePanelURLs(fromPanel []string, current string) []string {
	var out []string
	for _, u := range append(fromPanel, current) {
		u = strings.TrimRight(strings.TrimSpace(u), "/")
		if u != "" && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}
