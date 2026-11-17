# VPS 节点管理平台：设计方案

> 版本：v0.2 · 2026-11-16
> 定位：**自用**的探针监控 + Xray 节点管理 + 订阅平台

## 变更记录

| 版本 | 变更 |
|------|------|
| v0.1 | 初版通用方案 |
| v0.2 | 按已确认的前提重写：**自用**、**没有国内机器**、**只用 Xray**、**直连用 VLESS-Reality，落地用 Shadowsocks 2022**、**部分 VPS 只装探针**。删掉了多用户/计费/证书/CDN 等用不上的部分，新增服务器角色模型、线路（直连/链式）设计、无国内机的延迟与被墙检测方案、页面清单 |

---

## 1. 场景与范围

### 1.1 已确认的前提

| 项 | 结论 |
|----|------|
| 使用者 | 只有自己（单管理员） |
| 国内机器 | 没有。延迟监控以 **VPS 出发的回程视角** 为主 |
| 代理内核 | 只用 Xray-core |
| 直连协议 | VLESS + Reality + Vision（客户端只连这一种） |
| 落地协议 | Shadowsocks 2022，**只给入口机连**，不对外、不进订阅 |
| 机器类型 | 一部分只装探针，一部分装 Xray 当节点 |

### 1.2 做 / 不做

| 做 | 不做（或以后再说） |
|----|------------------|
| 探针监控、离线/阈值告警 | 多用户、套餐、配额、计费、用户自助页 |
| Reality 直连节点、SS 落地、链式线路 | VMess / Trojan / WS / CDN / 证书 |
| 回程延迟、线路段延迟、IP 被封检测 | 多管理员与角色权限 |
| 服务器流量（按 VPS 计费周期）+ 每条线路流量 | sing-box / Hysteria2 等其他内核 |
| Clash Meta (mihomo) / sing-box / Base64 订阅 | 外部订阅转换服务 |
| BBR 与网络调优 | |
| 面板远程更新 Agent、Xray、规则文件 | |
| 多渠道通知、Telegram Bot 查询 | |
| VPS 资产管理（价格、到期、续费提醒） | |

---

## 2. 服务器角色模型

每台 VPS 先有一个**安装模式**，节点机再勾选**能力**：

```
Server
├── mode: probe      仅探针，不装 Xray
└── mode: node       探针 + Xray
      ├── cap_reality  开启 Reality 入站（可当"直连节点"或"入口机"）
      └── cap_landing  开启 SS2022 入站（当"落地机"）
```

| 角色 | mode | 能力 | 作用 |
|------|------|------|------|
| 仅探针 | probe | 无 | 只做监控；也可以当延迟拨测的执行者、放面板 |
| 直连节点 | node | reality | 客户端 → 本机 Reality → 直接出网 |
| 入口机（中转） | node | reality | 客户端 → 本机 Reality → SS → 落地机 |
| 落地机 | node | landing | 接收入口机的 SS 流量 → 出网 |
| 入口 + 落地 | node | reality + landing | 同一台既能当直连/入口，又能给别的入口当落地 |

- 一台 Reality 机器可以同时承担"直连"和"入口"，区别只在线路配置（见第 5.4 节）。
- **仅探针 → 节点**：面板上点"升级为节点"，Agent 下载安装 Xray，不需要重装 Agent。
- **节点 → 仅探针**：停止并卸载 Xray，相关线路自动下线，订阅同步移除。
- 仅探针的机器在界面上隐藏所有 Xray 相关标签页，Xray 版本更新也会跳过它们。

---

## 3. 总体架构

```
         浏览器 / Telegram Bot                         客户端（mihomo / sing-box / v2rayN）
                │ HTTPS                                        │ 拉订阅
       ┌────────▼──────────────────────────────────────────────▼────────┐
       │  Panel（建议部署在一台"仅探针"的稳定 VPS 上）                    │
       │  Web UI · REST API · 订阅服务 · 告警引擎 · 配置生成器 · 调度器   │
       │  Agent Hub（gRPC 双向流）            SQLite (WAL)              │
       └─────▲───────────────────▲──────────────────────▲──────────────┘
             │ Agent 主动连出 TLS │                      │
   ┌─────────┴──────┐   ┌────────┴────────────┐   ┌─────┴───────────────┐
   │ VPS-A 仅探针    │   │ VPS-B 入口/直连      │   │ VPS-C 落地           │
   │ agent           │   │ agent + xray         │   │ agent + xray         │
   │                 │   │ Reality :443 ────────┼──▶│ SS2022 :随机端口     │
   │                 │   │                      │SS │ (仅允许 B 的 IP)     │
   └─────────────────┘   └──────────────────────┘   └──────────────────────┘
          ▲                       ▲
          └── 回程拨测 → 国内三网目标（电信/联通/移动）
```

**核心原则**

1. **Agent 主动连面板**：VPS 不开管理端口。
2. **面板是唯一配置来源**：Xray 配置全部由面板生成下发，本地手改会被识别为"配置漂移"并提示。
3. **面板挂了节点照常跑**：Agent 自治，未上报的数据存本地，恢复后补传。
4. **同一个 Agent 程序**，用 `mode` 区分是否管理 Xray。

---

## 4. 技术选型

| 组件 | 选型 |
|------|------|
| 面板后端 | Go 1.23+、Gin、GORM、SQLite（WAL）。自用规模不需要 PostgreSQL |
| Agent | Go 静态编译，amd64 / arm64；目标内存 < 20MB |
| 前端 | Vue 3 + Vite + Naive UI + ECharts；拓扑图用 Vue Flow；用 Go `embed` 打包进面板 |
| 通信 | gRPC 双向流 over TLS |
| Xray 交互 | Xray gRPC API：`HandlerService`（热增删用户/入站）、`StatsService`（流量）、`ObservatoryService`（出站探活与延迟） |
| 部署 | 单二进制 + systemd，前面放 Caddy 自动 HTTPS；也可 Docker Compose |

---

## 5. 模块详细设计

### 5.1 接入与 Agent

**一键安装**（面板生成，含一次性 Token）

```bash
# 仅探针
bash <(curl -fsSL https://panel.example.com/install.sh) --token <token> --mode probe
```

```bash
# 节点（同时安装 Xray）
bash <(curl -fsSL https://panel.example.com/install.sh) --token <token> --mode node
```

安装脚本流程：检测系统和架构 → 下载 Agent（校验 sha256 和签名）→ 用一次性 Token 换取长期凭证 → 注册为 systemd 服务（Alpine 用 OpenRC）→ node 模式额外安装 Xray 和规则文件。

**Agent 本地安全开关**（只能在 VPS 上改，面板改不了）

```yaml
# /etc/vpsagent/agent.yaml
mode: node                # probe | node
allow_remote_exec: false  # 是否允许面板下发任意命令 / WebSSH
allow_system_tune: true   # BBR / sysctl
allow_self_update: true
```

支持系统：Debian 11+、Ubuntu 20.04+、Rocky/Alma 9、Alpine 3.18+。

### 5.2 探针（系统监控）

| 类别 | 指标 |
|------|------|
| 基础 | 主机名、系统、内核、虚拟化类型、CPU 型号/核数、开机时长、IPv4/IPv6、地区/ASN |
| CPU | 使用率、steal（判断超售）、load 1/5/15 |
| 内存 | 内存、Swap |
| 磁盘 | 使用率、IO 速率 |
| 网络 | 实时上下行速率、累计流量、TCP/UDP 连接数 |
| Xray（仅节点机） | 进程状态、内存、CPU、版本、运行时长 |

- 采集 5 秒一次，上报 10 秒一次（可调）。
- 数据保留：原始 24 小时 → 1 分钟粒度 7 天 → 1 小时粒度 90 天 → 1 天粒度永久。
- 在线判定：30 秒无心跳标"疑似离线"，90 秒判定离线并告警。

**VPS 资产信息**（自用很实用）：服务商、商家后台链接、价格与币种、付费周期、到期日、流量额度与计费方式、备注。总览页显示每月总花费和即将到期的机器。

### 5.3 节点配置：Reality 入站 + SS2022 落地

**Reality 入站**（每台 cap_reality 机器一个）

| 字段 | 默认 |
|------|------|
| 端口 | 443（被占用时随机） |
| flow | `xtls-rprx-vision` |
| 密钥 | 面板生成 x25519 密钥对；私钥加密存储 |
| shortIds | 随机生成 1–2 个 |
| 目标站（target/dest + serverNames） | 从候选列表中由 Agent **在该 VPS 上实测**后推荐 |

**目标站检测**：Agent 从 VPS 本机对候选域名检测 TLS 1.3、H2、X25519 支持情况，记录握手延迟，并确认没有跳转到其他域名。优先推荐和 VPS 同地区、握手延迟低的站点，用户也可以手动填写。

**SS2022 落地入站**（每台 cap_landing 机器一个）

| 字段 | 默认 |
|------|------|
| 端口 | 随机高位端口 |
| method | `2022-blake3-aes-128-gcm`（可选 `aes-256-gcm` 版） |
| 服务端密钥 | 面板生成 |
| 用户 | **每台入口机一个独立用户**（方便统计每个入口的流量，也能单独吊销） |
| network | `tcp,udp` |
| 防火墙 | **只允许已关联入口机的 IP 访问**；入口机 IP 变化时 Agent 自动更新 nftables 集合 |

**下发流程**：面板生成完整配置 → Agent 写入临时文件 → `xray run -test` 校验 → 替换 → 应用（能热更新的走 API，否则重启）→ 回报结果。每次下发都保存一个配置版本，可以一键回滚。

### 5.4 线路设计：直连与链式

**核心思路：一台入口机只开一个 Reality 端口，用不同 UUID 区分线路。** Xray 路由支持按 `user`（email）匹配，所以：

- UUID-1 → 出站 `direct` → 直连线路
- UUID-2 → 出站 `to-jp01` → 经 JP-01 落地
- UUID-3 → 出站 `to-us01` → 经 US-01 落地

好处：只暴露一个 443 端口；新增或删除线路只需热增删一个用户，**不用重启 Xray、不断流**。

**线路（Route）模型**

```
Route {
  name:     "HK→JP"
  type:     direct | chain
  entry:    HK-01                 # Reality 所在机器
  landing:  JP-01                 # type=chain 时必填
  backup:   [JP-02]               # 可选，备用落地，自动切换
  uuid:     自动生成
  email:    "r-<route_id>"        # 用于路由匹配和流量统计
  enabled:  true
  sort, tags
}
```

**入口机 HK-01 生成的配置（示意）**

```json
{
  "api": { "tag": "api", "listen": "127.0.0.1:10085",
           "services": ["HandlerService", "StatsService", "ObservatoryService"] },
  "stats": {},
  "policy": {
    "levels": { "0": { "statsUserUplink": true, "statsUserDownlink": true } },
    "system": { "statsInboundUplink": true, "statsInboundDownlink": true,
                "statsOutboundUplink": true, "statsOutboundDownlink": true }
  },
  "inbounds": [{
    "tag": "reality-in", "port": 443, "protocol": "vless",
    "settings": {
      "decryption": "none",
      "clients": [
        { "id": "<uuid-1>", "flow": "xtls-rprx-vision", "email": "r-1" },
        { "id": "<uuid-2>", "flow": "xtls-rprx-vision", "email": "r-2" }
      ]
    },
    "streamSettings": {
      "network": "tcp", "security": "reality",
      "realitySettings": {
        "target": "<目标站>:443", "serverNames": ["<目标站>"],
        "privateKey": "<私钥>", "shortIds": ["<shortId>"]
      }
    },
    "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"], "routeOnly": true }
  }],
  "outbounds": [
    { "tag": "direct", "protocol": "freedom" },
    { "tag": "to-jp01", "protocol": "shadowsocks",
      "settings": { "servers": [{ "address": "<JP-01 IP>", "port": 23456,
        "method": "2022-blake3-aes-128-gcm", "password": "<服务端密钥>:<HK-01 专属用户密钥>" }] } },
    { "tag": "block", "protocol": "blackhole" }
  ],
  "observatory": { "subjectSelector": ["to-"], "probeURL": "https://www.gstatic.com/generate_204",
                   "probeInterval": "1m" },
  "routing": { "rules": [
    { "ip": ["geoip:private"], "outboundTag": "block" },
    { "protocol": ["bittorrent"], "outboundTag": "block" },
    { "user": ["r-2"], "outboundTag": "to-jp01" },
    { "user": ["r-1"], "outboundTag": "direct" }
  ]}
}
```

> 说明：较新的 Xray 中 Reality 的 `dest` 字段改名为 `target`（旧名仍兼容）。配置生成器要**按目标机器上的 Xray 版本**输出字段，下发前统一用 `xray run -test` 校验。

**落地机 JP-01 生成的配置（示意）**

```json
{
  "inbounds": [{
    "tag": "ss-in", "port": 23456, "protocol": "shadowsocks",
    "settings": {
      "method": "2022-blake3-aes-128-gcm",
      "password": "<服务端密钥>",
      "clients": [ { "password": "<HK-01 专属用户密钥>", "email": "from-hk01" } ],
      "network": "tcp,udp"
    }
  }],
  "outbounds": [ { "tag": "direct", "protocol": "freedom" } ]
}
```

（api / stats / policy 部分与入口机相同，省略。）

**备用落地自动切换**：线路配置了 `backup` 时，入口机生成 `balancer`（多个落地出站 + `observatory`），策略为 `leastPing`，路由规则改用 `balancerTag`。主落地挂掉会自动切到备用，同时发通知。

**落地出网分流**（可选，在落地机上配置）：比如某些 AI/流媒体域名改走 WARP 出站，或者屏蔽回国 IP。界面提供几个规则模板，按需勾选。

**拓扑页**：用图展示 入口 → 落地 的所有线路，每一段标注当前延迟和今日流量；任一段故障时整条线路标红。

### 5.5 延迟监控（没有国内机器的方案）

没有国内机器，就测不到"从国内到 VPS"的去程延迟。采用以下几个视角组合：

| # | 视角 | 怎么测 | 反映什么 |
|---|------|-------|---------|
| ① | **回程**（主力） | 每台 VPS 对国内三网目标做 TCP/ICMP 拨测 | VPS 回国线路质量（延迟、丢包、抖动） |
| ② | **线路段** | 入口机通过 Xray `ObservatoryService` 读取每个落地出站的存活状态和延迟；Agent 再补充对落地 SS 端口的 TCP 拨测 | 入口→落地这一段的质量 |
| ③ | **落地出网** | 落地机请求 `generate_204` 和几个常用站点 | 落地本身的出网质量 |
| ④ | VPS 互测（可选） | 任意两台 VPS 之间 TCP 拨测，生成延迟矩阵 | 帮你决定"哪台入口配哪台落地" |
| ⑤ | **家里电脑上报**（可选，后期） | 家里电脑上跑一个小程序，开机时调用本地 mihomo 的测速接口，把每个节点的延迟上报面板 | 唯一的真实国内视角；不开机就没有数据，不影响其他功能 |

**线路估算延迟** = ① 入口机回程延迟 + ② 入口→落地延迟，显示在线路列表和订阅节点名旁边（可选）。

**回程目标列表**：面板内置一份可编辑的国内目标列表（电信 / 联通 / 移动，按北京、上海、广州等地区分组）。默认目标需要在开发时实测挑选稳定可 ping 的地址，用户也可以自己增删。

**展示**：SmokePing 风格图（延迟分布 + 丢包着色）、24 小时/7 天切换、多台 VPS 对比。

**回程路由识别**（可选）：定期用 NextTrace 跑回程路由，自动打上 CN2 GIA、9929、4837、CMI 等线路标签。

### 5.6 被墙检测（没有国内机器的方案）

GFW 封 IP 通常是双向不通，所以**从 VPS 往国内也连不上**，可以从外面判断：

```
如果 某台 VPS 心跳正常（说明机器和海外网络都好）
   且 它对国内多个目标（覆盖三网、多个地区）的 TCP 拨测连续 N 轮全部失败
   且 同时段其他 VPS 对同样目标是通的（排除目标本身故障）
→ 判定"疑似 IP 被封"
→ 告警；把该入口相关线路在订阅中标记或剔除（可配置）
```

**局限**：封端口、协议干扰这类情况从海外测不出来。补救办法：

- 第 5.5 节⑤的家里电脑上报（开机时）能发现"国内连不上但 VPS 正常"。
- 面板上提供"我连不上了"按钮，一键在该 VPS 上跑诊断（Xray 状态、端口监听、回程拨测、近期日志），把结果汇总给你看。

### 5.7 流量统计

| 层级 | 数据来源 | 用途 |
|------|---------|------|
| 服务器 | `/proc/net/dev` 网卡计数 | 对照 VPS 商家流量额度（所有机器，含仅探针） |
| 线路 | 入口机 Xray 用户统计 `user>>>r-N>>>traffic` | 每条线路用了多少 |
| 落地 | 落地机 Xray 用户统计 `user>>>from-xxx>>>traffic` | 每台落地被哪些入口用了多少 |
| 出站 | 入口机 `outbound>>>to-xxx>>>traffic` | 校验链路流量 |

**采集要点**
- Agent 每 30–60 秒调用 `StatsService.QueryStats(reset=true)` 取增量，避免 Xray 重启清零。
- 网卡计数器要处理重启归零和回绕，同样转成增量。
- 每次上报带递增序号，面板按 `(agent_id, seq)` 去重；断线时 Agent 本地缓存（bbolt），恢复后补传。

**按 VPS 计费周期统计**（每台单独设置）

```
reset_day:   15                        # 每月 15 号重置
quota:       1 TB
count_mode:  out | in | both | max     # 商家计费口径
on_exceeded: alert | stop_xray         # 超额动作
```

告警档位：80% / 90% / 100%。还会按当前速度预测"预计 X 号用完"。

> 注意：链式线路的流量在入口机和落地机都会计一次（入口机进出各一次，落地机进出各一次）。商家计费以网卡为准，线路统计只用来看用量分布。

### 5.8 订阅

自用场景：**一个主订阅 + 可按设备生成多个订阅 Token**（手机、电脑各一个，方便分别查看访问记录和单独吊销）。所有设备共用同一套线路 UUID。

**地址**

```
https://sub.example.com/s/{token}                  # 按 User-Agent 自动识别格式
https://sub.example.com/s/{token}?target=mihomo    # 指定格式
```

**输出格式**：Clash Meta / mihomo YAML（Clash Verge Rev、FlClash、Stash 等）、sing-box JSON、Base64 分享链接（v2rayN、Shadowrocket 等）。

**内容规则**
- 只包含 Reality 线路（直连 + 链式），**落地 SS 永远不进订阅**。
- 节点命名模板：`{flag} {entry} → {landing}`，例如 `🇭🇰 HK-01 直连`、`🇭🇰→🇯🇵 HK-01 → JP-01`。
- 自动生成代理组（mihomo）：
  - 节点选择（手动）
  - 自动选择（url-test）
  - 按落地地区分组（JP / US / SG…）
  - 故障转移（fallback）
- 规则模板：内置一份国内直连 + AI / 流媒体分组的模板，也可以上传自己的模板。
- 可选：自动剔除离线或疑似被封的线路。
- 响应头带 `Subscription-Userinfo`（显示所有 VPS 本月流量汇总或你指定的某台）、`Profile-Update-Interval`。

**安全**
- Token 随机且足够长，可单独重置；限频；记录访问 IP 和 UA；出现陌生 IP 拉取时告警。
- 订阅格式转换全部在面板内完成，不经过第三方服务。
- **一键轮换**：重新生成所有线路 UUID、Reality 密钥和 shortId、SS 密钥 → 下发 → 客户端更新订阅即可。适用于怀疑订阅泄露的情况。

### 5.9 BBR 与系统优化

- 检测：内核版本（≥ 4.9）、当前拥塞控制和队列算法、虚拟化类型（OpenVZ/LXC 改不了内核参数，按钮置灰并说明原因）。
- 一键开启 BBR + fq：写入 `/etc/sysctl.d/99-vpspanel.conf`。
- TCP 调优：根据带宽和平均回程 RTT 计算缓冲区大小，提供"保守 / 均衡 / 激进"三档；修改前记录原值，可一键还原。
- 可选（带风险提示）：安装 XanMod 内核使用 BBRv3，需要重启。
- 系统初始化（可勾选）：时区与时间同步、Swap、ulimit、SSH 加固（改端口、禁用密码登录）、fail2ban。
- **仅探针机器也能用**（比如放面板的那台）。

### 5.10 面板远程更新

| 对象 | 适用机器 | 流程 |
|------|---------|------|
| Agent | 所有 | 面板发布版本 → 选范围（单台 / 分组 / 全部，可先更新 1 台试试）→ 下载 → 校验 sha256 + ed25519 签名 → 替换并保留旧版 → 重启 → **60 秒内连不回面板就自动回滚** |
| Xray | 仅节点机 | 面板定时拉取 GitHub Releases 并缓存 → 选版本（可锁定版本）→ 下载（可走面板中转）→ 校验 `.dgst` → 用新版跑 `xray run -test` 校验现有配置 → 替换并重启 → 健康检查失败自动回滚 |
| geoip / geosite | 仅节点机 | 定时更新，可选官方或增强版规则源 |
| 安装/运维脚本 | 所有 | 脚本统一由面板管理版本，执行前校验哈希 |
| 面板自身 | — | 检查新版本 → 先备份数据库 → 替换 → 自动迁移数据库 |

**版本兼容检查**：配置生成器知道每个功能需要的最低 Xray 版本，降级或创建线路时会提示。

### 5.11 通知

**渠道**：Telegram（主推）、Bark、企业微信、飞书、Email、通用 Webhook。

| 类别 | 事件 |
|------|------|
| 可用性 | VPS 离线 / 恢复；Xray 崩溃或自动重启；Agent 版本过旧 |
| 资源 | CPU / 内存 / 磁盘 / 负载超阈值并持续 N 分钟；CPU steal 过高 |
| 线路 | 回程延迟或丢包超阈值；**疑似 IP 被封**；入口→落地中断；切换到备用落地 |
| 流量 | VPS 流量达 80% / 90% / 100%；预计提前用完；流量异常突增 |
| 资产 | **VPS N 天后到期**（默认提前 7 天和 1 天提醒） |
| 安全 | 面板新 IP 登录；登录失败过多；订阅被陌生 IP 拉取 |
| 更新 | 有新 Xray / Agent / 面板版本；批量更新结果 |

**规则**：阈值 + 持续时长 + 冷却时间 + 静默时段（比如夜间只推严重告警）+ 恢复通知；多台同时离线时合并成一条消息。

**Telegram Bot 交互**（只响应绑定的 chat_id）：`/status` 总览、`/vps <名称>` 详情、`/traffic` 本月流量、`/routes` 线路状态、`/sub` 获取订阅链接。

### 5.12 安全

- 面板：强密码 + 2FA (TOTP)；登录限流；可设置随机后台路径；可限制只允许指定 IP 访问。
- 面板域名和订阅域名分开；面板可以不对公网开放（比如只通过代理访问）。
- **Agent 最小权限**：远程执行和 WebSSH 默认关闭，只能在 VPS 本地开启。这样即使面板被攻破，攻击者也拿不到所有 VPS 的 shell。
- 下发的二进制和脚本必须通过签名校验（ed25519，签名私钥离线保存）。
- Xray API 只监听 `127.0.0.1`；落地 SS 端口只对入口机 IP 开放。
- 敏感字段（Reality 私钥、SS 密钥、通知渠道 Token）用主密钥 AES-GCM 加密存库。
- 所有修改操作记审计日志。

### 5.13 运维工具（精简版）

| 功能 | 说明 |
|------|------|
| Xray 日志 | 实时查看 access / error 日志 |
| 服务操作 | 重启 Xray、重启 Agent |
| 诊断 | "我连不上了"一键诊断（见 5.6） |
| 测速 | VPS 之间 iperf3、Speedtest |
| 检测 | 流媒体/AI 解锁检测、IP 质量检测（主要给落地机用）、回程路由 |
| WebSSH / 批量脚本 | 需要在 VPS 本地开启 `allow_remote_exec` |
| 定时任务 | 定时重启 Xray、定时更新规则文件、定时跑检测 |

### 5.14 备份与回滚

- 每次配置下发保存一个版本，可查看差异并一键回滚。
- 数据库每日自动备份：本地保留 7 份，可选上传到 S3/R2/WebDAV 或发到 Telegram（加密后）。
- 整站迁移：在新服务器导入备份，Agent 自动切到新面板地址（面板地址支持配置多个备用地址）。

---

## 6. 通信协议（面板 ⇄ Agent）

- Agent 主动建立 gRPC 双向流（TLS）；断线后指数退避重连（1 秒到最多 60 秒，带随机抖动）。
- 注册：一次性 Token 换取 `agent_id + agent_secret`，之后用 HMAC 签名握手。

```protobuf
service AgentHub {
  rpc Connect(stream AgentMessage) returns (stream PanelMessage);
}

message AgentMessage {            // Agent → Panel
  oneof payload {
    Hello        hello        = 1;  // 版本、mode、系统信息、本地安全开关
    Metrics      metrics      = 2;
    TrafficDelta traffic      = 3;  // 带 seq
    ProbeResult  probe        = 4;
    TaskResult   task_result  = 5;  // 支持流式输出
    XrayState    xray_state   = 6;  // 配置哈希、版本、运行状态、Observatory 结果
    Event        event        = 7;  // Xray 崩溃、配置漂移等
  }
}

message PanelMessage {            // Panel → Agent
  oneof payload {
    ApplyConfig  apply_config = 1;  // 完整 Xray 配置 + 版本号
    XrayOp       xray_op      = 2;  // 热增删用户
    ProbeTasks   probe_tasks  = 3;
    Task         task         = 4;  // 升级为节点、更新、BBR、诊断等
    Ack          ack          = 5;
  }
}
```

任务状态：`pending → sent → running → success / failed / timeout`。Agent 离线时任务排队，上线后下发（任务可设过期时间）。

---

## 7. 数据模型

```
admin               (id, username, password_hash, totp_secret)
servers             (id, name, group, tags, mode[probe|node], cap_reality, cap_landing,
                     agent_id, agent_secret_hash, ipv4, ipv6, region, country_code,
                     provider, provider_url, price, currency, pay_cycle, expire_at,
                     reset_day, quota_bytes, count_mode, on_exceeded,
                     agent_version, xray_version, xray_locked_version, status, last_seen_at)
reality_inbounds    (server_id, port, target, server_names, private_key_enc, public_key,
                     short_ids)
landing_inbounds    (server_id, port, method, server_key_enc)
landing_users       (landing_server_id, entry_server_id, user_key_enc, email)
routes              (id, name, type[direct|chain], entry_server_id, landing_server_id,
                     backup_ids, uuid, email, enabled, sort, tags)
sub_tokens          (id, name/设备名, token_hash, target_default, last_used_at, enabled)
sub_templates       (id, target, name, content)
sub_access_logs     (token_id, ip, ua, ts)
metrics_raw / metrics_1m / metrics_1h / metrics_1d
probe_targets       (id, name, carrier, region, host, port, type)
probe_results_*     (target_id, server_id, ts, avg, min, max, jitter, loss)
segment_latency     (route_id / entry→landing, ts, delay, alive)    -- 来自 Observatory
traffic_hourly      (scope[server|route|landing_user|outbound], scope_id, hour, up, down)
traffic_seq         (agent_id, last_seq)
config_versions     (id, server_id, version, config_json, diff, created_at)
tasks               (id, server_id, type, payload, status, output, created_at, finished_at)
alert_rules / alert_events / notify_channels
releases            (component[agent|xray|geo], version, url, sha256, signature, channel)
audit_logs          (id, action, target, detail, ip, ts)
settings            (key, value)
```

---

## 8. 页面清单

| 页面 | 内容 |
|------|------|
| 总览 | 卡片墙（按分组/角色筛选）、在线数、本月总流量、本月花费、即将到期、未处理告警 |
| 服务器详情 | 监控曲线、回程延迟图、流量（计费周期进度条）、资产信息、Xray 状态（节点机）、操作按钮 |
| 线路 | 线路列表（类型、入口、落地、估算延迟、今日/本月流量、状态）、新建线路向导 |
| 拓扑 | 入口 → 落地 关系图，标注延迟和流量 |
| 延迟 | 回程 SmokePing 图、多机对比、VPS 互测矩阵 |
| 订阅 | Token 列表（设备）、格式预览、规则模板、访问日志、一键轮换 |
| 更新中心 | Agent / Xray / 规则文件版本、批量更新、更新记录 |
| 告警 | 规则、通知渠道、告警历史 |
| 工具 | 诊断、测速、解锁检测、日志、BBR/系统优化 |
| 设置 | 账号与 2FA、面板地址、备份、审计日志 |

**新建线路向导**：

1. 选择入口机。
2. 选择"直连"或"链式"。
3. 链式时选择落地机，可加备用落地。界面上显示入口→落地的延迟和落地机解锁情况，方便挑选。
4. 预览生成的配置变更。
5. 下发。
6. 自动连通性自检。
7. 加入订阅。

---

## 9. 项目目录结构

```
vps-panel/
├── cmd/
│   ├── panel/
│   └── agent/
├── internal/
│   ├── panel/
│   │   ├── api/            # REST
│   │   ├── hub/            # Agent 连接管理
│   │   ├── service/        # server / route / traffic / sub / asset
│   │   ├── xrayconf/       # 配置生成器（按 Xray 版本输出）+ 校验
│   │   ├── subscription/   # mihomo / sing-box / base64
│   │   ├── probe/          # 拨测任务分发、被墙判定
│   │   ├── alert/  notify/  scheduler/  release/  store/
│   ├── agent/
│   │   ├── collector/      # 系统指标
│   │   ├── xraymgr/        # 安装、进程、API（Handler/Stats/Observatory）
│   │   ├── prober/         # TCP/ICMP/HTTP 拨测、目标站检测
│   │   ├── firewall/       # nftables（落地端口白名单）
│   │   ├── tasks/  buffer/  updater/
│   └── common/
├── proto/
├── web/                    # Vue 3
├── scripts/                # install.sh
├── deploy/                 # docker-compose.yml、systemd unit
└── docs/
```

---

## 10. 开发路线图

| 阶段 | 范围 | 完成标志 |
|------|------|---------|
| **M1 探针** | Agent（probe 模式）、一键安装、系统监控、总览与详情页、资产信息与到期提醒、离线告警（Telegram）、登录 + 2FA | 所有 VPS 都能看到、掉线能收到通知 |
| **M2 节点与线路** | node 模式与"升级为节点"、Xray 安装管理、Reality 入站（含目标站检测）、SS2022 落地 + 防火墙白名单、直连/链式线路、按 UUID 热更新、配置版本与回滚 | 能在面板上建出直连和链式线路并实际可用 |
| **M3 流量与订阅** | 服务器/线路/落地流量、计费周期与超额告警、mihomo/sing-box/Base64 订阅、设备 Token、规则模板、一键轮换 | 客户端导入订阅日常使用 |
| **M4 延迟与被墙** | 回程拨测、Observatory 线路段延迟、备用落地自动切换、被墙判定、拓扑图、延迟图 | 线路质量一目了然，被封能收到通知 |
| **M5 运维** | Agent/Xray/规则文件远程更新（签名+回滚）、BBR 与调优、完整告警规则、多渠道通知、TG Bot、数据库备份 | 日常维护都能在面板完成 |
| **M6 可选** | 家里电脑上报、解锁/IP 质量/回程路由检测、WebSSH、测速、公开状态页 | 锦上添花 |

---

## 11. 技术风险与注意事项

| 风险 | 对策 |
|------|------|
| 没有国内视角，封端口/协议干扰测不出 | 被墙检测只承诺能发现"IP 被封"；配合"我连不上"诊断和可选的家里电脑上报 |
| Xray 重启后统计清零 | 始终 `reset=true` 拉增量 |
| Xray 不支持完整热重载 | 线路变更走用户热增删；只有改目标站、端口、路由模板时才重启 |
| Xray 配置字段随版本变化（如 `dest` → `target`） | 配置生成器按机器上的 Xray 版本输出；下发前 `xray run -test` |
| 落地 SS 端口被扫描 | nftables 只放行入口机 IP；入口 IP 变化自动同步 |
| 入口机 IP 变了（换 IP） | Agent 上报 IP 变化 → 自动更新落地白名单和订阅 → 通知 |
| 面板机故障 | 节点照常运行；数据库每日备份；支持多个面板备用地址 |
| 面板被攻破 | 远程执行默认关闭、签名校验、2FA、可限制面板访问 IP |
| 小内存 VPS | Agent 内存控制在 20MB 以内；采集间隔可调 |
| GitHub 访问慢或限流 | 面板缓存 Release 信息并提供中转下载 |

---

## 12. 已确认的决策（2026-11-16）

| 项 | 结论 |
|----|------|
| 前端 | Vue 3 + Naive UI |
| 面板位置 | 部署在一台"仅探针"的 VPS 上 |
| 规模 | 3–4 台入口 + 1 台落地，另有若干仅探针机器 |
| 主力客户端 | mihomo 系（Clash Verge Rev 等）：订阅先做 mihomo，sing-box / Base64 次之 |
| 家里电脑上报 | **不列入计划**（第 5.5 节 ⑤ 删除；被墙检测只依赖回程拨测 + 一键诊断） |
| 落地出网分流 | 暂不需要 WARP 或其他分流（第 5.4 节"落地出网分流"推迟） |

> 只有 1 台落地时，"备用落地自动切换"暂时用不上，但数据模型保留 `backup` 字段，以后加落地机可直接启用。

UI/UX 设计稿见 `docs/ui-design/`（`project/canvas.json` + 各画板 `.dc.html`），前端实现以它为准。
