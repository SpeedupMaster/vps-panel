# vps-panel

**专为自用场景打造的轻量级 VPS 探针监控 + Xray 节点与链式线路编排 + 多格式订阅中心。**

`vps-panel` 将「多机探针监控」、「VPS 资产账本」、「VLESS-Reality 直连与 Shadowsocks 2022 链式落地编排」、「三网回程拨测与被墙检测」以及「内置订阅转换服务」整合在单个二进制文件中，无需外部依赖即可完成从机器接入、性能监控、组网分流到客户端订阅下发的全流程闭环。

- 📐 架构与设计方案：[docs/plan.md](docs/plan.md)
- 🎨 UI/UX 设计稿：[docs/ui-design/](docs/ui-design/project/canvas.json)
- 🌐 三网拨测节点快照：[docs/zstatic-nodes.snapshot.json](docs/zstatic-nodes.snapshot.json)（每日自动从 `zstaticcdn.com` 同步）

---

## ✨ 核心特性

### 1. 极简、安全、自治的架构设计
- **单二进制开箱即用**：后端采用 Go + Gin + SQLite（WAL 模式），前端（Vue 3 + Naive UI + ECharts）通过 `embed` 打包进面板二进制，无需额外部署数据库或 Redis。
- **Agent 主动回连**：所有受控 VPS 通过 gRPC 双向流（HTTP/2 / TLS）主动连接面板，**VPS 无需开放任何管理端口**。
- **超低资源占用**：Agent 静态编译（支持 `linux/amd64` 与 `linux/arm64`），内置软内存上限控制（目标常驻内存 `< 20MB`），256MB 小内存机器亦可流畅运行。
- **离线自治与断点续传**：面板维护或宕机期间，各节点 Xray 服务照常运行；监控与流量增量自动落盘至本地 `bbolt` 缓存，恢复连接后按序号自动补传，统计不丢不重。
- **最小权限控制**：远程命令执行与 WebSSH **默认关闭**，且只能通过在 VPS 本地修改 `/etc/vpsagent/agent.yaml` 开启——即使面板失守，攻击者也无法获取受控机器 Shell。

### 2. 全维度系统探针与 VPS 资产管理
- **实时性能监控**：采集 CPU（含判断超售的 `steal` 指标与系统负载）、内存与 Swap、磁盘空间与 I/O 速率、实时网卡吞吐、TCP/UDP 连接数以及 Xray 进程状态。
- **多级时序归档**：监控数据自动降采样归档（原始粒度保留 24 小时 → 1 分钟粒度 7 天 → 1 小时粒度 90 天 → 1 天粒度永久保存），兼顾细节回溯与磁盘体积。
- **VPS 资产账本**：记录服务商、后台链接、购买价格、币种、付款周期与到期时间，总览页自动折算月度总支出，并在到期前自动发送续费提醒。
- **公开状态页**：可选开启 `/status` 公开探针页，对外展示所选机器的在线状态、运行时长、资源占用与三网回程延迟（自动隐藏 IP、服务商等敏感信息）。

### 3. Xray 节点与链式（中转）线路编排
- **灵活的服务器角色**：
  - **仅探针（`probe`）**：仅运行系统监控与拨测，不安装 Xray，界面自动隐藏节点相关配置。
  - **节点机（`node`）**：按需开启 **Reality 入站**（作为直连节点或中转入口）和 **SS2022 入站**（作为落地机），支持在面板上一键无损切换「仅探针 ⇄ 节点」。
- **单端口多路复用 & 零断流热更新**：
  - 每台入口机仅需暴露一个 `VLESS + Reality + XTLS-Vision`（默认 `:443`）端口，通过不同 `UUID`（按 Xray `user` 路由规则）区分「直连出网」或「转发至指定落地机」。
  - 新增、启停或删除线路时，通过 Xray gRPC `HandlerService` **动态热增删用户，无需重启 Xray、不中断现有连接**。
- **Reality 伪装目标站实测推荐**：Agent 直接从目标 VPS 本机对候选域名实测 TLS 1.3、HTTP/2、X25519 支持情况与握手延迟，自动排除重定向站点并推荐最佳伪装目标。
- **落地端口隐身防护**：落地机采用 `Shadowsocks 2022`，永远不进入客户端订阅；Agent 自动通过 `nftables` 维护白名单，**仅允许已关联入口机的 IP 访问落地端口**，入口机更换 IP 时自动同步更新防火墙与订阅。
- **备用落地自动切换**：链式线路支持配置备用落地机，基于 Xray `Observatory`（`leastPing`）实时探活，主落地故障时自动切换至备用节点并推送告警。
- **配置防漂移与一键回滚**：每次配置下发前均经过 `xray run -test` 校验并保存历史版本，支持版本 Diff 对比与一键回滚；本地手动篡改配置文件会被立即识别为「配置漂移」。

### 4. 无国内机视角的延迟监控与被墙检测
- **三网回程拨测**：无需额外部署国内探针，由各 VPS 对国内电信 / 联通 / 移动目标发起 TCP/ICMP 拨测，提供 **SmokePing 风格**延迟与丢包分布图、多机横向对比及 VPS 互测延迟矩阵。
- **全链路延迟估算**：结合「入口机回程延迟」与「Observatory 实测入口→落地段延迟」，直观呈现整条链式线路的真实质量与可视化拓扑图。
- **智能 IP 被封判定**：当某台 VPS 海外心跳正常，但对国内三网多地区目标连续拨测失败（且同期其他 VPS 拨测正常）时，自动判定为「疑似 IP 被封」，立即触发告警并可自动从订阅中剔除相关线路。

### 5. 精细化流量统计与自包含订阅中心
- **四维流量统计**：同时采集服务器网卡流量（`/proc/net/dev`，对齐商家计费账单）、单条线路流量、落地机各入口来源流量与出站流量。
- **个性化计费周期与配额保护**：每台 VPS 可独立设置账单重置日、月度流量配额、计费口径（单向出/入、双向合计、进出取大值）及超额动作（告警通知或自动停用 Xray），并按当前速率预测耗尽日期。
- **原生多格式订阅**：面板内置订阅渲染引擎（不依赖第三方 subconverter），支持根据客户端 `User-Agent` 自动识别或通过参数指定输出 **Clash Meta (mihomo)**、**sing-box**、**Base64 通用订阅**。
- **设备级 Token 与一键全量轮换**：支持按手机、电脑等设备签发独立订阅 Token，记录每次拉取的 IP 与 UA，发现陌生 IP 拉取即刻告警；怀疑泄露时可**一键重新生成所有线路 UUID、Reality 密钥对与 SS 密钥**并同步下发。

### 6. 运维工具箱、安全更新与异地备份
- **实用工具箱**：「我连不上了」一键全项诊断、流媒体与 AI 解锁检测、IP 质量检测、NextTrace 三网回程路由追踪、Speedtest 测速、VPS 间 `iperf3` 内网/跨国测速、BBR + `fq` 开启与基于回程 RTT 的三档 TCP 缓冲区调优（支持一键还原）、实时 Xray 日志查看及 WebSSH 终端。
- **带回滚机制的远程更新**：在面板内集中管理并批量更新 Agent、Xray-core、GeoIP/GeoSite 规则文件及面板自身；支持 SHA256 / `.dgst` / Ed25519 离线签名校验，Agent 更新后 **60 秒内未能连回面板将自动回滚至旧版本**。
- **多渠道告警与 Telegram Bot**：支持 Telegram、Bark、企业微信、飞书、SMTP 邮件与通用 Webhook（支持 HMAC 签名），具备持续时长判定、冷却时间、夜间静默时段与恢复通知；内置 Telegram Bot 支持交互式查询机器、流量、线路与订阅。
- **加密自动备份与整站迁移**：每日通过 `VACUUM INTO` 生成在线数据库快照，支持使用密码进行 `scrypt + AES-GCM` 加密（`.vpb`），可自动上传至 **S3 / Cloudflare R2、WebDAV 或发送至 Telegram**；导入备份即可完成整站迁移。

---

## 🏗️ 系统架构与角色模型

```text
          浏览器 / PWA / Telegram Bot                   客户端（mihomo / sing-box / v2rayN）
                      │ HTTPS                                           │ 拉取订阅 (/:token)
       ┌──────────────▼─────────────────────────────────────────────────▼──────────────┐
       │  vps-panel（建议部署在一台稳定 VPS 上，单端口同时承载 HTTP / WebSocket / gRPC） │
       │  Web UI · REST API · 订阅渲染器 · 告警引擎 · Xray 配置生成器 · 定时任务调度器  │
       │  Agent Hub（gRPC 双向流 over HTTP/2）                         SQLite (WAL)    │
       └──────▲──────────────────────────────▲──────────────────────────▲──────────────┘
              │ Agent 主动连出               │                          │
   ┌──────────┴─────────┐       ┌────────────┴───────────┐   ┌──────────┴──────────────┐
   │ VPS-A [仅探针]      │       │ VPS-B [入口 / 直连]     │   │ VPS-C [落地机]           │
   │ mode: probe        │       │ mode: node (reality)   │   │ mode: node (landing)    │
   │ 仅监控 + 回程拨测   │       │ Reality :443 ──────────┼──▶│ SS2022 :随机高位端口     │
   └────────────────────┘       └────────────────────────┘   │ (nftables 仅放行 B IP)  │
                                                             └─────────────────────────┘
```

| 角色定位 | `mode` | 节点能力 | 说明 |
| :--- | :--- | :--- | :--- |
| **仅探针** | `probe` | 无 | 仅采集系统监控指标与执行三网回程拨测，不安装 Xray |
| **直连节点** | `node` | `cap_reality` | 客户端 → 本机 Reality 入站 → 直接出网（`freedom`） |
| **中转入口机** | `node` | `cap_reality` | 客户端 → 本机 Reality 入站 → 按 UUID 分流至对应落地机 SS2022 |
| **落地机** | `node` | `cap_landing` | 仅接收入口机转发来的 SS2022 流量并出网，不直接暴露给客户端 |
| **混合节点** | `node` | `cap_reality` + `cap_landing` | 同一台机器既可作为直连/中转入口，也可作为其他入口机的落地节点 |

---

## 🚀 快速开始（部署面板）

GitHub Actions 会在每次推送到 `main` 分支或打版本标签时先执行前后端测试与构建门禁（`pnpm test` / `pnpm build`、`go vet` 与 `go test -race`），通过后自动构建 `linux/amd64` 与 `linux/arm64` 多架构镜像并发布至 GitHub Container Registry（`ghcr.io/speedupmaster/vps-panel:latest`）。镜像内已预置 `amd64` 与 `arm64` 双架构 Agent 二进制包，启动后即可直接供受控机器下载安装。

### 方式一：Docker Compose + Caddy 自动 HTTPS（推荐）

无需克隆代码仓库，在服务器上新建目录并下载部署配置即可：

```bash
mkdir -p vps-panel && cd vps-panel
curl -fsSLO https://raw.githubusercontent.com/SpeedupMaster/vps-panel/main/deploy/docker-compose.yml
curl -fsSLO https://raw.githubusercontent.com/SpeedupMaster/vps-panel/main/deploy/Caddyfile.docker
curl -fsSL https://raw.githubusercontent.com/SpeedupMaster/vps-panel/main/deploy/.env.example -o .env
```

编辑 `.env` 文件，修改域名与面板地址：

```env
PANEL_DOMAIN=panel.example.com
VPSPANEL_URL=https://panel.example.com
VPSPANEL_ADMIN=admin
# VPSPANEL_PASSWORD=    # 留空则在首次启动时自动生成随机密码并打印到日志
TZ=Asia/Shanghai
```

> 💡 **独立订阅域名（可选）**：若希望将管理后台与订阅域名分离，可取消注释 [`Caddyfile.docker`](deploy/Caddyfile.docker) 中的 `sub.example.com` 配置段并替换为你的订阅域名，同时在面板「订阅 → 订阅域名」中填入 `https://sub.example.com`。面板与示例 Caddyfile 均会限制该独立域名仅响应订阅链接，后台页面、管理 API（`/api/*`）与 Agent gRPC（`/vpspanel.AgentHub/*`）一律返回 `404`。

启动服务并查看初始管理员密码：

```bash
docker compose up -d
docker compose logs panel
```

### 方式二：仅部署面板容器（已有反向代理或本地端口访问）

如果服务器已运行 Nginx / Caddy / 1Panel / Cloudflare Tunnel，可使用 [`deploy/docker-compose.panel.yml`](deploy/docker-compose.panel.yml)：

```bash
mkdir -p vps-panel && cd vps-panel
curl -fsSL https://raw.githubusercontent.com/SpeedupMaster/vps-panel/main/deploy/docker-compose.panel.yml -o docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/SpeedupMaster/vps-panel/main/deploy/.env.example -o .env
# 默认仅监听 127.0.0.1:8080 供本机反代访问；若反代经 Docker 网桥或外部节点转发，请在 .env 中按实际源 IP/CIDR 配置 VPSPANEL_TRUSTED_PROXIES
docker compose up -d
docker compose logs panel
```

> ⚠️ **反向代理配置要点**：面板在单端口上同时承载 HTTP REST API、SSE 实时事件流（`/api/events`）、WebSocket（WebSSH）以及 **gRPC over HTTP/2（`h2c`）**。
> - **Caddy**：请使用 `reverse_proxy h2c://127.0.0.1:8080`，仅对 gRPC / SSE / WebSSH 路径启用无超时流式转发，常规 HTTP 请求保留有界超时（完整示例见 [`deploy/Caddyfile`](deploy/Caddyfile)）。
> - **Nginx**：请确保开启 `http2` 并为 gRPC 路径或反代配置长连接与 HTTP/2 转发支持。

### 方式三：直接使用 `docker run`

```bash
docker pull ghcr.io/speedupmaster/vps-panel:latest
docker run -d --name vps-panel \
  --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v ./data:/data \
  -e VPSPANEL_URL=https://panel.example.com \
  -e TZ=Asia/Shanghai \
  ghcr.io/speedupmaster/vps-panel:latest
```

### 方式四：二进制 + Systemd 原生部署

1. 编译或下载 `vps-panel` 二进制文件及 `agent/` 目录（含 `vpsagent-linux-amd64` 与 `vpsagent-linux-arm64`）至 `/opt/vps-panel`。
2. 参考 [`deploy/vps-panel.env.example`](deploy/vps-panel.env.example) 创建 `/etc/vps-panel.env`，参考 [`deploy/vps-panel.service`](deploy/vps-panel.service) 配置 systemd 服务：
   ```bash
   sudo useradd -r -s /sbin/nologin vpspanel
   sudo mkdir -p /opt/vps-panel/agent /var/lib/vps-panel
   sudo chown -R root:root /opt/vps-panel
   sudo chown -R vpspanel:vpspanel /var/lib/vps-panel
   sudo cp deploy/vps-panel.service /etc/systemd/system/
   sudo systemctl daemon-reload && sudo systemctl enable --now vps-panel
   ```

### 面板配置参数与环境变量

面板所有命令行标志均支持通过同名环境变量配置：

| 命令行参数 | 环境变量 | 默认值 | 说明 |
| :--- | :--- | :--- | :--- |
| `-data` | `VPSPANEL_DATA` | `./data` | 持久化数据目录（数据库、主密钥、备份、Agent 包） |
| `-listen` | `VPSPANEL_LISTEN` | `:8080` | 监听地址（同端口复用 HTTP + gRPC h2c/h2） |
| `-url` | `VPSPANEL_URL` | 空 | 面板公网访问地址（如 `https://panel.example.com`） |
| `-admin` | `VPSPANEL_ADMIN` | `admin` | 首次初始化时的管理员用户名 |
| `-password` | `VPSPANEL_PASSWORD` | 空（随机生成） | 首次初始化时的管理员密码，留空时输出到启动日志 |
| `-trusted-proxies` | `VPSPANEL_TRUSTED_PROXIES` | `127.0.0.1,::1` | 受信任的反向代理 IP 或 CIDR 列表（逗号分隔） |
| `-tls-cert` / `-tls-key` | `VPSPANEL_TLS_CERT` / `KEY` | 空 | 直接启用 TLS 时的证书与私钥路径（前置反代时无需设置） |
| — | `VPSPANEL_AGENT_DIR` | `<二进制目录>/agent` | 预置 Agent 二进制目录（支持原始或 `.gz` 文件），启动时由 `RegisterBundledAgent` 直接注册并从此目录提供下载，不重复复制到 `<data>/agent/` |

---

## 🖥️ 接入与管理 Agent

支持系统：**Debian 11+**、**Ubuntu 20.04+**、**Rocky / AlmaLinux 9+**、**Alpine 3.18+**（自动适配 `systemd` 与 `OpenRC`），支持 `x86_64 (amd64)` 与 `aarch64 (arm64)` 架构。

### 1. 安装 Agent

在面板点击 **「添加服务器」** 后，可选择以下任一方式接入：

- **方式 A（复制一键命令）**：在目标 VPS 上以 `root` 执行面板生成的命令（含一次性注册 Token）：
  ```bash
  # 仅探针模式（只监控，不装 Xray）
  bash <(curl -fsSL https://panel.example.com/install.sh) --panel https://panel.example.com --token <一次性Token> --mode probe

  # 节点模式（监控 + 自动安装并接管 Xray）
  bash <(curl -fsSL https://panel.example.com/install.sh) --panel https://panel.example.com --token <一次性Token> --mode node
  ```
- **方式 B（面板 SSH 远程自动安装）**：在添加服务器弹窗中直接填入 VPS 的 SSH 地址、端口与密码/私钥，由面板一次性登录执行安装并实时流式输出安装日志（**SSH 凭证仅用于本次连接，绝不落盘存储**）。

### 2. Agent 本地安全开关

出于安全设计，以下高权限能力**只能在受控 VPS 本机修改**，面板端只能读取状态而无法远程篡改。配置文件位于 `/etc/vpsagent/agent.yaml`：

```yaml
mode: node                 # probe | node
allow_remote_exec: false   # 是否允许面板下发任意 Shell 命令与使用 WebSSH（默认关闭）
allow_system_tune: true    # 是否允许面板执行 BBR / sysctl 网络调优
allow_self_update: true    # 是否允许面板远程更新 Agent 二进制
```

你也可以在 VPS 上直接使用 `vpsagent` 命令行修改并重启生效：

```bash
vpsagent config set allow_remote_exec true
systemctl restart vpsagent
```

### 3. 卸载 Agent

在受控 VPS 上以 `root` 执行：

```bash
# 仅卸载 Agent 服务与二进制（保留 Xray 继续运行，现有代理线路不受影响）
bash <(curl -fsSL https://panel.example.com/install.sh) --uninstall

# 彻底清除（同时卸载 Xray、清理 nftables 防火墙表、还原 sysctl 调优并删除所有配置数据）
bash <(curl -fsSL https://panel.example.com/install.sh) --uninstall --purge
```

---

## 🛠️ 日常维护与命令行工具

### 常用 Docker 维护命令

```bash
# 更新面板至最新镜像
docker compose pull && docker compose up -d

# 忘记密码或丢失两步验证（2FA）设备时，一键重置管理员密码并关闭 2FA
docker compose exec panel vps-panel reset-admin
```

### 持久化数据目录结构（`./data`）

所有状态均保存在数据目录中，备份或迁移服务器时只需打包该目录（或在面板「设置 → 备份」中导出）：

| 路径 | 说明 |
| :--- | :--- |
| `panel.db` | SQLite 主数据库（WAL 模式） |
| `master.key` | 主加密密钥（用于加密数据库中存储的 Reality 私钥、SS 密钥、通知渠道凭证等，**切勿丢失**） |
| `backups/` | 每日自动备份与手动创建的数据库快照（`.tar.gz` 或加密的 `.vpb`） |
| `agent/` | 供节点下载或远程更新的 `vpsagent-linux-{amd64,arm64}` 二进制、`.sha256` 与 `.sig` 签名文件 |

### 离线 Ed25519 签名（可选高阶安全特性）

若希望严格防范面板被攻破后向节点下发恶意更新包，可使用 `vps-panel` 生成离线 Ed25519 密钥对：

```bash
# 1. 在本地安全环境生成私钥 sign.key，终端会输出 Base64 公钥（填入面板「设置 → 签名公钥」）
vps-panel keygen sign.key

# 2. 发布自定义 Agent 二进制前进行离线签名（生成对应的 .sig 文件放入 data/agent/）
vps-panel sign sign.key dist/vpsagent-linux-amd64 dist/vpsagent-linux-arm64
```

Agent 安装时会将公钥写入本地 `/etc/vpsagent/sign.pub`（重装时若提供了新的签名公钥会同步更新；兼容 PKIX SPKI 与 32 字节原始 Base64 Ed25519 公钥），后续任何无有效签名的更新包均会被拒绝执行。

---

## 📱 特色功能使用指南

### 1. 客户端订阅地址与参数

在面板「订阅」页面创建或编辑设备订阅时可自定义链接后缀（留空则自动生成随机后缀），订阅地址格式如下（同时继续兼容旧版 `/s/<token>`）：

```text
https://panel.example.com/<自定义后缀>                  # 根据客户端 User-Agent 自动返回对应格式
https://panel.example.com/<自定义后缀>?target=mihomo    # 强制输出 Clash Meta (mihomo) YAML
https://panel.example.com/<自定义后缀>?target=singbox   # 强制输出 sing-box JSON
https://panel.example.com/<自定义后缀>?target=base64    # 强制输出 Base64 通用节点链接
```

- 响应头自动附带 `Subscription-Userinfo`（展示流量用量与到期时间）与 `Profile-Update-Interval`。
- 内置国内直连 + AI / 流媒体分流规则模板，支持在面板中自定义编辑模板内容。

### 2. Telegram Bot 交互指令

在「告警 → 通知渠道」中添加 Telegram 渠道并开启「响应 Bot 命令」后，可在绑定的 Chat 中直接发送以下命令：

- `/status` — 查看所有 VPS 在线状态、实时 CPU/网速与三网平均回程延迟
- `/vps <名称>` — 查看指定 VPS 的详细监控指标、Xray 状态与账单流量进度
- `/traffic` — 查看所有 VPS 本计费周期流量汇总与每条线路的消耗排行
- `/routes` — 查看所有直连与链式线路的存活状态及估算延迟
- `/sub` — 快速获取当前启用的订阅链接

### 3. 移动端与 PWA 独立应用

- **移动端自适应**：屏幕宽度 `≤ 767px` 时自动切换为移动端专属布局（顶部状态栏 + 底部「总览 / 线路 / 延迟 / 告警 / 更多」导航栏），复杂表格自动转为卡片列表，WebSSH 终端内置常用快捷键栏（`Esc`、`Tab`、`Ctrl+C`、方向键等）。
- **PWA 安装**：支持通过浏览器「添加到主屏幕」作为独立 App 运行；Service Worker 仅预缓存静态资源并对页面导航采用网络优先（`NetworkFirst`，仅离线回退且不覆盖服务端 `404`，完全兼容动态自定义后台路径），`/api`、实时事件流、WebSSH 与订阅请求始终直连面板，面板升级后温和提示刷新。

---

## 💻 从源码构建与本地开发

### 环境要求
- Go `1.27+`
- Node.js `22+` & `pnpm`（支持通过 `corepack enable` 自动启用）

### 编译构建

仓库提供了开箱即用的 [`Makefile`](Makefile)：

```bash
make all      # 构建前端并交叉编译面板与 Agent（支持 make -j 并行构建，panel 自动等待 web 完成）
make web      # 构建前端 SPA 并输出至 internal/panel/webui/dist 供 Go embed 打包
make panel    # 编译面板二进制：dist/vps-panel-linux-{amd64,arm64}（自动先构建 web）
make agent    # 编译 Agent 二进制：dist/vpsagent-linux-{amd64,arm64}
make test     # 运行 go vet、Go 单元测试，并在存在 web/node_modules 时运行前端测试
```

如果使用 Docker Compose 从源码本地构建启动：

```bash
cd deploy
cp .env.example .env
docker compose -f docker-compose.yml -f docker-compose.build.yml up -d --build
```

### 本地前后端联调

```bash
# 终端 1：启动后端面板（默认监听 :8080）
go run ./cmd/panel -data ./data -url http://localhost:5173

# 终端 2：启动前端 Vite 开发服务器（自动将 /api 与 /s/ 代理至 localhost:8080）
cd web && pnpm install && pnpm dev
```

### 目录结构

```text
vps-panel/
├── cmd/
│   ├── panel/                  # 面板主程序入口、CLI 子命令（reset-admin / keygen / sign）
│   └── agent/                  # Agent 主程序入口（register / run / config / version）
├── internal/
│   ├── common/
│   │   ├── proto/              # 面板 ⇄ Agent gRPC 双向流通信协议定义
│   │   ├── xrayconf/           # Xray 配置生成器（按版本自适应字段、支持直连/链式/负载均衡）
│   │   └── version/            # 编译期版本信息注入
│   ├── panel/
│   │   ├── api/                # Gin REST API 路由、鉴权中间件、公开状态页、SPA 静态服务
│   │   ├── auth/               # 会话管理、TOTP 两步验证、恢复码、登录限流与 IP 白名单
│   │   ├── hub/                # Agent gRPC 连接池、HMAC 握手认证与消息分发
│   │   ├── service/            # 核心业务逻辑（服务器、线路、流量、拨测、告警、备份、TG Bot 等）
│   │   ├── sub/                # 订阅渲染器（mihomo / sing-box / Base64）与内置规则模板
│   │   ├── notify/             # 多渠道通知发送器（Telegram / Bark / 企微 / 飞书 / 邮件 / Webhook）
│   │   ├── scheduler/          # 后台定时任务调度器
│   │   ├── secret/             # 主密钥管理与 AES-GCM 敏感字段加解密
│   │   ├── store/              # GORM + SQLite 数据模型与自动迁移
│   │   └── webui/              # 前端构建产物 Go embed 挂载点
│   └── agent/
│       ├── collector/          # Linux 系统指标、网卡流量与公网 IPv4/IPv6 采集
│       ├── xraymgr/            # Xray 安装、进程守护与 gRPC API 交互（Handler/Stats/Observatory）
│       ├── prober/             # TCP/ICMP/HTTP 回程拨测与 Reality 伪装目标站扫描检测
│       ├── firewall/           # 落地机 nftables 端口白名单自动管理
│       ├── tasks/              # 远程运维任务执行器（诊断、解锁检测、NextTrace、BBR、WebSSH、自更新）
│       ├── buffer/             # 断线期间流量与监控增量本地持久化（bbolt）
│       └── config/             # Agent 本地凭证与安全开关管理
├── web/                        # 前端工程（Vue 3 + Vite + Naive UI + ECharts + xterm.js + PWA）
├── scripts/                    # Agent 一键安装与卸载脚本（install.sh）
├── deploy/                     # Docker、Docker Compose、Caddyfile 与 systemd 部署文件
└── docs/                       # 架构设计方案、UI 设计稿与三网拨测节点快照
```
