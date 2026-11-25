package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"vpspanel/internal/common/proto"
	"vpspanel/internal/panel/scheduler"
	"vpspanel/internal/panel/store"
)

// ---------- scripts ----------

// BuiltinScripts cover the "system init" items of the plan; they run through
// the exec task, so the VPS must enable allow_remote_exec locally.
var BuiltinScripts = []store.Script{
	{Name: "时区设为 Asia/Shanghai 并开启时间同步", Content: `set -e
timedatectl set-timezone Asia/Shanghai 2>/dev/null || ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
if command -v timedatectl >/dev/null; then timedatectl set-ntp true || true; fi
command -v chronyd >/dev/null || (apt-get install -y -qq chrony 2>/dev/null || dnf install -y -q chrony 2>/dev/null || apk add -q chrony 2>/dev/null || true)
date`},
	{Name: "添加 1G Swap（已有则跳过）", Content: `set -e
if swapon --show | grep -q .; then echo "已有 swap"; swapon --show; exit 0; fi
fallocate -l 1G /swapfile 2>/dev/null || dd if=/dev/zero of=/swapfile bs=1M count=1024
chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
grep -q '^/swapfile' /etc/fstab || echo '/swapfile none swap sw 0 0' >> /etc/fstab
sysctl -w vm.swappiness=10 && echo 'vm.swappiness=10' > /etc/sysctl.d/98-swap.conf
free -h`},
	{Name: "提高文件句柄上限（ulimit）", Content: `set -e
cat > /etc/security/limits.d/99-vpspanel.conf <<'EOF'
* soft nofile 1048576
* hard nofile 1048576
root soft nofile 1048576
root hard nofile 1048576
EOF
mkdir -p /etc/systemd/system.conf.d
printf '[Manager]\nDefaultLimitNOFILE=1048576\n' > /etc/systemd/system.conf.d/99-vpspanel.conf
systemctl daemon-reexec 2>/dev/null || true
echo "完成，新登录会话生效"`},
	{Name: "安装 fail2ban（保护 SSH）", Content: `set -e
command -v fail2ban-server >/dev/null || (apt-get update -qq && apt-get install -y -qq fail2ban) || dnf install -y -q fail2ban || apk add -q fail2ban
cat > /etc/fail2ban/jail.d/sshd-vpspanel.conf <<'EOF'
[sshd]
enabled = true
maxretry = 5
findtime = 10m
bantime = 1h
EOF
(systemctl enable --now fail2ban && systemctl restart fail2ban) || rc-service fail2ban restart
fail2ban-client status sshd || true`},
	{Name: "SSH 加固：禁用密码登录（确认已配置密钥！）", Content: `set -e
if [ ! -s /root/.ssh/authorized_keys ]; then echo "root 没有 authorized_keys，为防止锁死已中止"; exit 1; fi
mkdir -p /etc/ssh/sshd_config.d
printf 'PasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin prohibit-password\n' > /etc/ssh/sshd_config.d/99-vpspanel.conf
grep -q '^Include /etc/ssh/sshd_config.d' /etc/ssh/sshd_config || sed -i '1i Include /etc/ssh/sshd_config.d/*.conf' /etc/ssh/sshd_config
sshd -t && (systemctl reload sshd 2>/dev/null || systemctl reload ssh 2>/dev/null || rc-service sshd reload)
echo "已禁用密码登录"`},
	{Name: "安装 XanMod 内核（BBRv3，需重启，有风险）", Content: `set -e
# Optional per the plan: a new kernel can fail to boot on some providers.
# Make sure the provider offers a VNC/rescue console before running this.
. /etc/os-release
case "$ID" in debian|ubuntu) ;; *) echo "仅支持 Debian / Ubuntu，当前 $ID"; exit 1;; esac
[ "$(uname -m)" = "x86_64" ] || { echo "仅支持 x86_64"; exit 1; }
virt=$(systemd-detect-virt 2>/dev/null || echo none)
case "$virt" in lxc|openvz|docker|podman|wsl) echo "$virt 容器无法更换内核"; exit 1;; esac
flags=$(grep -m1 '^flags' /proc/cpuinfo)
echo "$flags" | grep -qw sse4_2 || { echo "CPU 不支持 x86-64-v2，无法安装 XanMod"; exit 1; }
level=x64v3
for f in avx2 bmi2 fma movbe; do echo "$flags" | grep -qw $f || { level=x64v2; break; }; done
apt-get update -qq && apt-get install -y -qq gnupg wget ca-certificates
install -d -m 0755 /etc/apt/keyrings
wget -qO - https://dl.xanmod.org/archive.key | gpg --dearmor --yes -o /etc/apt/keyrings/xanmod-archive-keyring.gpg
echo 'deb [signed-by=/etc/apt/keyrings/xanmod-archive-keyring.gpg] http://deb.xanmod.org releases main' > /etc/apt/sources.list.d/xanmod-release.list
apt-get update -qq && apt-get install -y -qq linux-xanmod-$level
echo "已安装 linux-xanmod-$level。确认无误后手动重启：reboot"
echo "重启后在面板「工具 → 网络调优」里开启 BBR + fq，内核会使用 BBRv3"`},
}

// SeedScripts adds each built-in script once, so upgrades pick up new ones
// while scripts the user deleted stay deleted.
func (p *Panel) SeedScripts() {
	seeded := map[string]bool{}
	_ = json.Unmarshal([]byte(p.St.Get("scripts_seeded", "")), &seeded)
	if len(seeded) == 0 {
		var have []store.Script
		p.St.DB.Select("name").Find(&have)
		for _, s := range have {
			seeded[s.Name] = true
		}
		if len(have) > 0 {
			for _, s := range BuiltinScripts[:5] {
				seeded[s.Name] = true
			}
		}
	}
	for _, s := range BuiltinScripts {
		if seeded[s.Name] {
			continue
		}
		s := s
		if p.SaveScript(&s) == nil {
			seeded[s.Name] = true
		}
	}
	b, _ := json.Marshal(seeded)
	_ = p.St.Set("scripts_seeded", string(b))
}

func (p *Panel) ListScripts() []store.Script {
	var ss []store.Script
	p.St.R().Order("id").Find(&ss)
	return ss
}

func (p *Panel) SaveScript(s *store.Script) error {
	if strings.TrimSpace(s.Name) == "" || strings.TrimSpace(s.Content) == "" {
		return errors.New("名称和内容必填")
	}
	if len(s.Content) > 64<<10 {
		return errors.New("脚本过大")
	}
	s.Content = strings.ReplaceAll(s.Content, "\r\n", "\n")
	sum := sha256.Sum256([]byte(s.Content))
	s.SHA256 = hex.EncodeToString(sum[:])
	return p.St.DB.Save(s).Error
}

// DeleteScript deletes a script after verifying no Schedule references it,
// and records an AuditLog entry.
func (p *Panel) DeleteScript(id uint, ip string) error {
	var s store.Script
	if err := p.St.DB.First(&s, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var refs int64
	if err := p.St.DB.Model(&store.Schedule{}).Where("action = ?", fmt.Sprintf("script:%d", id)).Count(&refs).Error; err != nil {
		return err
	}
	if refs > 0 {
		return errors.New("有定时任务正在引用该脚本，请先修改或删除相关定时任务")
	}
	if err := p.St.DB.Delete(&store.Script{}, id).Error; err != nil {
		return err
	}
	p.Audit("system", "delete_script", s.Name, "", ip)
	return nil
}

// RunScript sends a saved script (or ad-hoc content when id is 0) to servers.
func (p *Panel) RunScript(id uint, content string, serverIDs []uint, timeout int) (string, []uint64, error) {
	if id != 0 {
		var s store.Script
		if err := p.St.DB.First(&s, id).Error; err != nil {
			return "", nil, err
		}
		content = s.Content
	}
	if strings.TrimSpace(content) == "" || len(serverIDs) == 0 {
		return "", nil, errors.New("缺少脚本或服务器")
	}
	if timeout <= 0 || timeout > 3600 {
		timeout = 300
	}
	sum := sha256.Sum256([]byte(content))
	batch := fmt.Sprintf("script-%d", time.Now().UnixNano())
	var ids []uint64
	for _, sid := range serverIDs {
		t, err := p.CreateTask(sid, proto.TaskExec, map[string]string{"sha256": hex.EncodeToString(sum[:])}, []byte(content), timeout, batch)
		if err != nil {
			return batch, ids, err
		}
		ids = append(ids, t.ID)
	}
	return batch, ids, nil
}

// ---------- schedules ----------

// ScheduleActions are what a schedule may run.
var ScheduleActions = map[string]string{
	"restart_xray": "重启 Xray", "update_geo": "更新规则文件", "unlock": "解锁检测", "ip_quality": "IP 质量检测",
	"speedtest": "测速", "diagnose": "诊断", "trace": "回程路由",
}

func (p *Panel) ListSchedules() []store.Schedule {
	var ss []store.Schedule
	p.St.R().Order("id").Find(&ss)
	return ss
}

func (p *Panel) SaveSchedule(s *store.Schedule) error {
	if err := ValidateSchedule(s); err != nil {
		return err
	}
	if s.ID != 0 {
		var old store.Schedule
		if p.St.DB.First(&old, s.ID).Error == nil {
			s.LastRun = old.LastRun
		}
	}
	return p.St.DB.Save(s).Error
}

func (p *Panel) DeleteSchedule(id uint) error {
	return p.St.DB.Delete(&store.Schedule{}, id).Error
}

func (p *Panel) TriggerSchedule(id uint) error {
	var s store.Schedule
	if err := p.St.DB.First(&s, id).Error; err != nil {
		return ErrNotFound
	}
	p.RunScheduleNow(&s)
	return p.St.DB.Model(&s).Update("last_run", time.Now()).Error
}

func (p *Panel) scheduleServers(spec string) []uint {
	var svs []store.Server
	q := p.St.DB.Model(&store.Server{})
	switch spec {
	case "", "all":
	case "nodes":
		q = q.Where("mode = ?", "node")
	case "landings":
		q = q.Where("cap_landing = ?", true)
	case "entries":
		q = q.Where("cap_reality = ?", true)
	default:
		q = q.Where("id IN ?", parseIDs(spec))
	}
	q.Find(&svs)
	var ids []uint
	for _, sv := range svs {
		ids = append(ids, sv.ID)
	}
	return ids
}

// ValidateSchedule checks the cron spec: "HH:MM" daily, "HH:MM@1,3,5"
// weekdays (0 = Sunday) or "@every 6h".
func ValidateSchedule(s *store.Schedule) error {
	if s.Name == "" {
		return errors.New("名称必填")
	}
	if strings.HasPrefix(s.Action, "script:") {
		if _, err := strconv.Atoi(strings.TrimPrefix(s.Action, "script:")); err != nil {
			return errors.New("脚本无效")
		}
	} else if ScheduleActions[s.Action] == "" {
		return errors.New("动作无效")
	}
	if _, _, err := parseCron(s.Cron); err != nil {
		return err
	}
	return nil
}

type cronSpec struct {
	every    time.Duration
	hm       int
	weekdays map[time.Weekday]bool
}

func parseCron(c string) (*cronSpec, bool, error) {
	c = strings.TrimSpace(c)
	if d, ok := strings.CutPrefix(c, "@every "); ok {
		dur, err := time.ParseDuration(strings.TrimSpace(d))
		if err != nil || dur < 10*time.Minute {
			return nil, false, errors.New("间隔至少 10 分钟，例如 @every 6h")
		}
		return &cronSpec{every: dur}, true, nil
	}
	hm, days, _ := strings.Cut(c, "@")
	h, m, ok := scheduler.ParseHM(hm)
	if !ok {
		return nil, false, errors.New("时间格式应为 HH:MM、HH:MM@1,3,5 或 @every 6h")
	}
	cs := &cronSpec{hm: h*60 + m}
	if days != "" {
		cs.weekdays = map[time.Weekday]bool{}
		for _, d := range strings.Split(days, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(d))
			if err != nil || n < 0 || n > 6 {
				return nil, false, errors.New("星期取值 0-6")
			}
			cs.weekdays[time.Weekday(n)] = true
		}
	}
	return cs, false, nil
}

func scheduleDue(s *store.Schedule, now time.Time) bool {
	cs, interval, err := parseCron(s.Cron)
	if err != nil {
		return false
	}
	if interval {
		return now.Sub(s.LastRun) >= cs.every-30*time.Second
	}
	if cs.weekdays != nil && !cs.weekdays[now.Weekday()] {
		return false
	}
	return now.Hour()*60+now.Minute() == cs.hm && now.Sub(s.LastRun) > 2*time.Minute
}

// RunSchedules is called every minute.
func (p *Panel) RunSchedules() {
	var ss []store.Schedule
	p.St.DB.Where("enabled = ?", true).Find(&ss)
	now := time.Now()
	for i := range ss {
		if scheduleDue(&ss[i], now) {
			p.St.DB.Model(&ss[i]).Update("last_run", now)
			p.RunScheduleNow(&ss[i])
		}
	}
}

func (p *Panel) RunScheduleNow(s *store.Schedule) {
	ids := p.scheduleServers(s.Servers)
	if sid, ok := strings.CutPrefix(s.Action, "script:"); ok {
		id, _ := strconv.Atoi(sid)
		_, _, _ = p.RunScript(uint(id), "", ids, 0)
		return
	}
	for _, id := range ids {
		if Tools[s.Action].Node {
			var sv store.Server
			if p.St.DB.Select("mode").First(&sv, id).Error != nil || sv.Mode != "node" {
				continue
			}
		}
		_, _ = p.RunTool(id, s.Action, nil)
	}
}
