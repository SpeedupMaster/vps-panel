# vps-panel

自用的 VPS 探针监控 + Xray 节点管理 + 订阅面板。

- 设计方案：[docs/plan.md](docs/plan.md)
- UI/UX 设计稿：[docs/ui-design/](docs/ui-design/project/canvas.json)
- 回程拨测目标：来自 [zstaticcdn.com](https://www.zstaticcdn.com/) 的三网节点目录（`/api/v1/DescribeAllNodes`），面板每日同步，内置快照见 [docs/zstatic-nodes.snapshot.json](docs/zstatic-nodes.snapshot.json)

## 组成

| 目录 | 说明 |
|---|---|
| `cmd/panel` | 面板（Go、Gin、GORM + SQLite），内嵌前端 |
| `cmd/agent` | Agent（探针 / 节点两种模式），主动连面板 |
| `internal/common/proto` | 面板 ⇄ Agent 通信协议（gRPC 双向流，JSON 编码） |
| `web` | 前端（Vue 3 + Naive UI + ECharts） |
| `scripts` | 一键安装脚本 |
| `deploy` | systemd、Docker Compose、Caddy 示例 |

## 构建

```bash
make web      # 构建前端并放入面板的 embed 目录
make panel    # dist/vps-panel-linux-{amd64,arm64}
make agent    # dist/vpsagent-linux-{amd64,arm64}
```

开发时 `cd web && pnpm dev`，`/api` 与 `/s/` 代理到本机 `:8080` 的面板。

## 手机与 PWA

- 宽度 ≤ 767px 时切换为手机布局：顶栏 + 底部 4 个常用入口（总览 / 线路 / 延迟 / 告警）+「更多」抽屉；表格改为卡片列表，适配刘海屏安全区，输入框不会触发 iOS 自动缩放；WebSSH 带一排常用按键（Esc、Tab、Ctrl+C、方向键…）。
- 可「添加到主屏幕」作为独立 App 使用（manifest + Service Worker，图标由 `web/scripts/gen-icons.mjs` 生成）。Chrome / Edge / Android 上侧栏会出现「安装应用」；iOS Safari 点它会提示从「分享 → 添加到主屏幕」安装。
- Service Worker 只缓存界面文件；`/api`、实时事件、WebSSH、订阅（`/s/`）和安装/下载地址始终直连面板，不会读到过期数据。
- 面板升级后，打开的页面会提示「面板已更新」，点「刷新」切换到新版本，不会在填写表单时被强制刷新。
- 切到后台再回来、或网络恢复时，自动重连实时事件并刷新服务器状态；断网时底部显示提示。
- PWA 需要 HTTPS（或 localhost）；按 `deploy/` 里的 Caddy 示例部署即可。

## 卸载 Agent

在 VPS 上以 root 执行：

```bash
bash <(curl -fsSL https://面板域名/install.sh) --uninstall          # 只卸载 Agent，Xray 继续运行，已有线路不受影响
bash <(curl -fsSL https://面板域名/install.sh) --uninstall --purge  # 同时清除 Xray、落地防火墙规则、BBR 调优和 Agent 数据
```

## 公开状态页

默认关闭。在「设置 → 面板 → 公开状态页」开启后，任何人都能访问 `https://面板域名/status`：显示所选服务器的在线状态、运行时长、CPU / 内存 / 硬盘占用和三网回程延迟，每分钟自动刷新。页面不显示 IP、服务商和版本，不链接到面板，也不受「后台路径」和「允许访问的 IP」限制。
