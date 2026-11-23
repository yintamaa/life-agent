# life-agent

**个人生活大一统的 Agent：你生活里散落各处的信息收进一个中心，自动整理成"今天要关心什么"，再让 Agent 帮你处理。**

- **信息都在一个地方**：
  - 消息：微信、飞书、QQ、Discord、多个邮箱，后续还有 Telegram、iMessage 和短信；
  - 生活数据：账单、快递、行程、日程、健康、智能家居，规划中。
- **自动整理**：分拣出"需要看"、@我、告警；从短信和邮件里识别快递取件码、账单、行程、订阅（规划中）；每天一份"今天"简报。
- **Agent 帮你处理**：任意会话一键"交给 Agent"。Agent 跑在你各台机器上，驱动 Claude Code / Codex，每一步有副作用的操作都等你批准，结果再回到原会话。
- **一个中心，多端访问**：常开的 Mac 上跑一个 hub，手机、电脑经 Tailscale 访问，界面风格仿 Multica / Linear。

产品规划（数据源目录、生活场景、优先级）见 [docs/product-life.md](docs/product-life.md)。

## 和 OpenClaw / Persona / botmux / Multica / HappyClaw 有什么不同

| | 任务从哪来 | Agent 拿得到的上下文 | 存 IM 正文 | 微信 / QQ 聊天记录 | 重量 |
|---|---|---|---|---|---|
| OpenClaw | IM 私聊汇入一个 AI 助手 | 和助手的对话 | 只存 AI 对话 | ✗ | 重 |
| Persona | 和主 Agent 对话；Inbox | Inbox 分诊上下文、知识库（按天存全文，回填需审批） | 知识库存（365 天） | ✗（微信只有 iLink bot，没有 QQ） | 重 |
| botmux | 飞书话题（一个话题一个 CLI） | 当前话题 | ✗（留在飞书） | ✗ | 中 |
| Multica | issue 看板 | issue 内容 | ✗ | ✗ | 中重 |
| HappyClaw | Web 和 8 个 IM 机器人里与 Bot 对话、定时任务 | 和 Bot 的会话 | 只存 Bot 会话 | ✗（都是官方 Bot 通道） | 中重（多用户 Node 单体） |
| **life-agent** | **全渠道消息**、看板、主 Agent | **完整的 IM 历史和全文搜索** | ✅ | ✅ | 轻 |

其他项目都把 IM 当作给 Agent 下指令的入口；我们把**你的生活数据**当作核心：先汇总、先整理，Agent 是帮你打理的手。工作编排只是其中一块。

- 详细的定位、多端同步对比和借鉴清单：[docs/competitive-analysis.md](docs/competitive-analysis.md)
- Agent 协同设计（草案）：[docs/design-agents.md](docs/design-agents.md)

## 架构

```
 连接器（每个账号一个，进程内 goroutine）
 ┌ feishu ┐┌ wechat ┐┌  qq   ┐┌ mail:gmail ┐┌ mail:work ┐ …
 └───┬────┘└───┬────┘└───┬───┘└─────┬──────┘└─────┬─────┘
     └─────────┴── model.Conversation / model.Message ──┘
                          │
                 Hub：SQLite + FTS5(trigram)
                 去重 · 未读 · 分拣(triage) · 告警识别 · 搜索
                          │
                 HTTP API + SSE  ──  React UI（嵌入二进制）
                          │
               Tailscale ── 手机 / 电脑 / iPad
```

- **单一 hub**：常开的 Mac 上跑一个进程，所有数据只存在这里。各设备只是访问它的窗口，不在设备之间同步数据。为什么 hub 是刚需，见竞品分析第 3 节。
- **连接器只负责搬运数据**：实现 `internal/connector.Connector` 即可接入新渠道，UI 和分拣逻辑不用动。

## 渠道

| 渠道 | 接入方式 | 读 | 发 | 实时性 | 状态 |
|---|---|---|---|---|---|
| 飞书 | `lark-cli` 以**本人身份**调 OpenAPI | ✅ | ✅（`feishu.send`） | 轮询，默认 30s | 已在真实账号上验证 |
| 微信 | 读**已解密**的 4.x 本地库镜像（本项目不做解密） | ✅ | — | 轮询，默认 10s | 用伪造的解密库测过 |
| QQ | OneBot v11 正向 WS（NapCatQQ） | ✅ | ✅（`qq.send`） | 实时推送 | 只编译通过，未测试 |
| 邮件 | IMAP，多账号；按线程归并为会话 | ✅ | — | 轮询，默认 60s | 只测过解析部分 |
| Discord | Bot（Gateway 实时 + REST 补拉历史） | ✅ Bot 所在服务器的频道（含历史）、发给 Bot 的私信 | ✅（`discord.send`） | 实时 | 用伪造事件测过，未接真实服务器 |

各渠道的配置步骤、风险和取舍见 [docs/connectors.md](docs/connectors.md)。

## 快速开始

依赖：Go（`make` 会通过 `GOTOOLCHAIN` 自动下载所需版本）、Node 22 和 pnpm。用飞书需要先装好并登录 `lark-cli`。

```bash
cp config.example.yaml config.yaml   # 按需启用渠道
make build                           # 前端 + 单一二进制 bin/lifeagent
./bin/lifeagent -config config.yaml  # 打开 http://127.0.0.1:7788
make test
```

- 前端开发：`make dev` 起后端，另开 `cd web && pnpm dev`（`/api` 代理到 7788）。
- 脱离后端预览：`VITE_MOCK=1 pnpm dev`，使用示例数据。
- 常开的 Mac 上常驻运行：见 `deploy/com.lifeagent.hub.plist`。

## 多端访问

hub 默认只监听 `127.0.0.1`。其它设备通过 Tailscale 访问：

```bash
tailscale serve --bg --https=8443 http://127.0.0.1:7788
```

然后在 `config.yaml` 打开 `auth.tailscale`，只放行你自己的 Tailscale 账号。hub 会用 `tailscale whois` 反查请求来源，不会只凭请求头放行。

多端一致性：
- 已读、已处理状态存在 hub 上，所有设备共享。
- 实时更新通过 SSE 推送，断线重连后自动补发漏掉的变更。
- "标记已处理"带版本校验：处理期间来了新消息，就不会被一起清掉。
- 发送带幂等键：网络抖动重试不会重复发送。

## Agent 协同

在任意会话里点"交给 Agent"，写一句指示，选好在哪台机器、哪个工作区、用 Claude Code 还是 Codex。Agent 会带着这个会话最近的消息开工；做完给出总结，如果需要回复原会话，会附一份草稿，你确认后才会发出去。

```
消息 ──"交给 Agent"──► 任务（「待办」）──► node：git worktree + Claude Code / Codex
  ▲                                         │ 需要审批的操作 → 手机 / 电脑，先到者生效
  └──────── 回复草稿，你确认后发回原会话 ◄───────┘
```

### 跑 Agent 的机器（node）

- **hub 本机**：在 `config.yaml` 里设置 `node.enabled: true`，配置运行时和工作区。
- **其它机器**（比如工作电脑，公司代码只在这台机器上被读写）：

  ```bash
  cp node.example.yaml node.yaml      # 填 hub_url（tailscale serve 地址）和 token
  ./bin/lifeagent node -config node.yaml
  ```

  - node 主动外连 hub，不需要开放入站端口。
  - 事件先写进本地的 SQLite outbox，断线重连后从上次确认的位置续传，不丢也不重复。

每个任务在独立的 git worktree（分支 `life-agent/<任务ID>`）里执行，并行任务互不干扰；产物留在分支上，由你决定合不合并。

### Agent 预设

在 `config.yaml` 的 `agents:` 下写好常用的 Agent：名字、跑在哪台机器、哪个仓库、用哪个 CLI、固定指令。手机上派活只要选一个名字。界面会实时显示每个 Agent 能不能用，比如"节点 work-laptop 不在线"。也可以选"自定义"，临时指定机器和仓库。

### 手机上管理（PWA + 推送）

1. 让 hub 经 `tailscale serve` 提供 HTTPS 访问（见"多端访问"）。
2. **iPhone**：用 Safari 打开 `https://<机器名>.<tailnet>.ts.net:8443/#/todo`，点"分享 → 添加到主屏幕"，**从主屏幕图标打开**，再在待办页点"开启通知"。iOS 只允许主屏幕上的网页应用接收推送。
   **Android / 电脑**：直接在待办页点"开启通知"。
3. 点"测试"确认能收到推送。之后：
   - Agent 需要审批时会立即推送。通知上有"允许 / 拒绝"按钮（Android 和电脑支持；iOS 点开通知后在页面里操作），多台设备上先点的生效；
   - 任务完成或失败时推送，同一个任务的新通知会替换旧的。

推送内容端到端加密，推送服务只看得到密文。VAPID 密钥首次启动时自动生成，存在 `data_dir/vapid.json`。

### 审批（默认：执行，但每一步有副作用的操作都要你批准）

| 自动放行 | 需要审批 | 直接拒绝 |
|---|---|---|
| 只读工具（Read、Grep、Glob 等）；修改**任务工作区内**的文件；不含管道或重定向的白名单命令（`git status`、`go test` 等） | 其余命令（如 `git push`、带重定向的命令）、改工作区外的文件、联网抓取、MCP 工具 | 命中禁止规则（`rm -rf /`、`git push --force`、`sudo` 等） |

- **Claude Code**：每个工具调用都经过 PreToolUse hook（`lifeagent hook`）。需要审批的会推到所有设备，先点的生效；超时自动拒绝。hook 出任何异常都按拒绝处理，绝不放行。
- **Codex**：无交互模式没有让人审批的通道，所以改用 `workspace-write` 沙箱兜底：只能写任务工作区，默认不能联网。

规则都在 `node.approval` 下可调。设计细节见 [docs/design-agents.md](docs/design-agents.md)。

> 隐私提示：从会话派任务时，最近的消息会作为上下文发给模型服务商（Anthropic / OpenAI）。公司会话请先确认是否符合公司规范。

## 分拣规则（`internal/triage`）

- **需要看**：未回复的私聊、@我、VIP 发送者。你在会话里发言即自动清除，也可以手动"标记已处理"。
- **线上告警**：标题命中告警关键词的会话，或机器人告警消息占多数（至少 3 条，且至少占一半）的会话。告警会话不出现在其它栏。
- **历史回填**：首次接入时回填的历史视为已读。之后的新消息，包括 hub 停机后补拉的，照常计未读。
- 规则都在 `config.yaml` 的 `triage:` 下可调。

## 路线图

- [x] 连接器框架：飞书、微信（只读）、QQ、多邮箱
- [x] 收件箱界面：需要看 / 全部 / 未读 / @我 / 私聊 / 群聊 / 线上告警，全文搜索
- [x] 多端同步 v1：Tailscale 鉴权、变更序号续传、已处理版本校验、发送幂等
- [ ] 消息推送：会话变成"需要看"时也推送（审批和任务推送已完成）
- [ ] LLM 分拣：分成"需要处理 / 知悉 / 噪音"三档
- [x] Agent 运行时适配：Claude Code（逐步审批）、Codex（沙箱）；每个任务一个 git worktree
- [x] node 模式：远程机器主动外连 hub，durable outbox 断线续传
- [x] 任务（「待办」模块）：从消息一键转任务、手机审批（先到者生效）、回复草稿确认后发回
- [x] 跨端多 Agent 管理：Agent 预设、待办页新建任务、PWA 加推送（审批和完成通知，通知里直接审批）
- [ ] node 承载连接器：飞书跑在工作电脑上（需要先决定数据是否离开工作电脑，见设计文档）
- [ ] 规则触发任务：如告警栏里的卡片自动触发诊断 skill
- [ ] 主 Agent 拆解任务，多 node 并行
- [ ] 更多渠道：iMessage、Telegram

## 目录

```
cmd/lifeagent/          主程序：hub（默认）、`node`（独立节点）、`hook`（审批钩子）
internal/model/         平台无关的数据模型
internal/connector/     连接器接口 + feishu / wechat / qq / mail
internal/store/         SQLite 存储、视图、搜索、变更序号
internal/triage/        分拣规则
internal/agent/         Agent CLI 适配：Claude Code、Codex
internal/node/          节点：worktree、审批策略、hook、与 hub 的链路（outbox）
internal/work/          hub 侧编排：任务、节点注册、审批、消息转任务
internal/api/           REST + SSE + 鉴权 + 静态资源
web/                    Vite + React 前端（构建产物嵌入二进制）
docs/                   渠道接入说明、竞品分析、Agent 协同设计
deploy/                 launchd 常驻配置
```
