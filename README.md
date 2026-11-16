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
