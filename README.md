# life-agent

**个人消息数据层：全渠道收件箱 + 分拣 + 搜索。**

你的消息散落在微信、飞书、QQ 和好几个邮箱里。life-agent 把它们**原样收进一个地方**：统一存储、统一去重和计未读、自动分出"需要看"和"线上告警"、跨渠道全文搜索。手机和电脑通过一个常驻的 hub 协同使用。界面风格仿 Multica / Linear。

它是一个更大的"个人一站式工作平台"的第一块。待办、日记、账本等模块以后都会建立在这层消息数据之上。

## 和 OpenClaw / Persona / botmux 有什么不同

这些项目都以 **Agent 为中心**，IM 只是给 AI 的输入输出渠道。它们不存、也读不到你和朋友之间的微信或 QQ 聊天记录。life-agent 反过来，**把消息本身当作一等数据**。

| | 消息在系统里的角色 | 存 IM 正文 | 微信 / QQ 聊天记录 |
|---|---|---|---|
| OpenClaw | 和 AI 助手对话的渠道 | 只存 AI 对话 | ✗ |
| Persona | 主 Agent 的输入；Inbox 只存摘要 | ✗ | ✗ |
| botmux | 远程操控编码 CLI 的指令 | ✗（留在飞书） | ✗ |
| **life-agent** | **数据本身** | ✅ | ✅ |

详细的定位、多端同步对比和借鉴清单见 [docs/competitive-analysis.md](docs/competitive-analysis.md)。

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

## 多端访问（开发中，见路线图）

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

## 分拣规则（`internal/triage`）

- **需要看**：未回复的私聊、@我、VIP 发送者。你在会话里发言即自动清除，也可以手动"标记已处理"。
- **线上告警**：标题命中告警关键词的会话，或机器人告警消息占多数（至少 3 条，且至少占一半）的会话。告警会话不出现在其它栏。
- **历史回填**：首次接入时回填的历史视为已读。之后的新消息，包括 hub 停机后补拉的，照常计未读。
- 规则都在 `config.yaml` 的 `triage:` 下可调。

## 路线图

- [x] 连接器框架：飞书、微信（只读）、QQ、多邮箱
- [x] 收件箱界面：需要看 / 全部 / 未读 / @我 / 私聊 / 群聊 / 线上告警，全文搜索
- [ ] 多端同步 v1：Tailscale 鉴权、变更序号续传、已处理版本校验、发送幂等（开发中）
- [ ] 推送：只在会话变成"需要看"时推（Web Push / Bark）
- [ ] LLM 分拣：分成"需要处理 / 知悉 / 噪音"三档
- [ ] agent 模式：连接器分散到其它机器（工作电脑上的飞书、虚拟机里的 QQ）
- [ ] 从消息里抽取待办，接入「待办」模块
- [ ] 更多渠道：iMessage、Telegram

## 目录

```
cmd/lifeagent/          主程序：加载配置、启动连接器、HTTP
internal/model/         平台无关的数据模型
internal/connector/     连接器接口 + feishu / wechat / qq / mail
internal/store/         SQLite 存储、视图、搜索、变更序号
internal/triage/        分拣规则
internal/api/           REST + SSE + 鉴权 + 静态资源
web/                    Vite + React 前端（构建产物嵌入二进制）
docs/                   渠道接入说明、竞品分析
deploy/                 launchd 常驻配置
```
