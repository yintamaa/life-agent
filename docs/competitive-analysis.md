# 竞品分析：多端 IM 协同与个人 hub

> 调研时间：2026-11-18。调研方式：拉取源码只读分析，关键结论都附了出处文件。没有运行任何竞品代码。
>
> | 项目 | 分析版本 |
> |---|---|
> | [OpenClaw](https://github.com/openclaw/openclaw) | `6b5bc83ae` |
> | [Persona](https://github.com/daidr/persona) | `4aaef55`；Inbox 部分参考了更新约 40 个提交的上游分支 |
> | [botmux](https://github.com/deepcoldy/botmux) | `697de2f` |
> | [botmux-platform](https://github.com/botmux-ai/botmux-platform) | `bd7faf3`（v0.1.0） |
> | [Multica](https://github.com/multica-ai/multica) | 仅作界面参考 |

## 1. 定位：大家在解决不同的问题

这几个项目常被放在一起讨论，因为它们都有"hub""多端""接飞书 / 微信"这些关键词。但如果问**"消息"在系统里扮演什么角色**，它们的差别就很清楚了。

### OpenClaw：IM 是 AI 助手的"嘴和耳朵"

- **要解决的问题**：让一个 AI 助手同时出现在你的 WhatsApp、Telegram、iMessage、Discord 等渠道里，任何渠道发来的私聊都汇入同一个主会话，助手在原渠道回复。
- **消息的角色**：消息是**和 AI 对话的输入输出**。Gateway 存的是"人与助手"的对话记录（每个 agent 一个 SQLite，事件按序号存储），而不是你和朋友之间的聊天记录。
- **多端**：手机、Mac、网页都是 Gateway 的客户端；手机还能作为 node，向助手提供拍照、定位、录屏等能力。
- **一句话**：**个人 AI 助手的网关**。

### Persona：IM 是"主 Agent 调度器"的一个输入源

- **要解决的问题**：你在一个对话里提需求，主 Agent 拉取上下文、拆解任务，派给你各台电脑上的 Claude Code / Codex；你在手机上看实时进度、审批工具调用，回到桌前接着干。
- **消息的角色**：
  - **Inbox** 从飞书、邮件、GitHub 等收集"需要你处理的事"，由小模型分成"需要你处理 / 知悉 / 噪音"三档。
  - 它只存收件箱条目和摘要，**不存 IM 正文**。
  - 微信只是一个 iLink bot 通道，只转发你自己发的消息，**不读你的微信聊天记录**。
  - 开源仓库里的飞书网关已移入私有扩展。
- **多端**：iOS、Watch、Mac、Android、Web 五端原生客户端，配套自签证书、配对码、APNs 中继（推送内容 HPKE 加密）、电脑主动外连隧道，体系非常完整。
- **代价**：1.2 万个文件，Postgres，hub 与 manager 双进程，迭代极快（16 天约 3500 个提交）。对"只想要一个 IM 收件箱"来说太重。
- **一句话**：**多机 Agent 编排平台，带一个工作收件箱**。

### botmux / botmux-platform：IM 是"远程终端"

- **要解决的问题**：把飞书变成编码 CLI 的遥控器。每个私聊、群或话题对应一个 Claude Code / Codex 会话，终端输出实时渲染成飞书卡片，手机上也能接着写代码。
- **消息的角色**：
  - 飞书消息是**给 CLI 的指令**。
  - 消息的权威数据就在飞书本身，会话状态存在各机器本地的 SQLite。
- **多端**：
  - 单机模式：飞书卡片、网页终端、本地 tmux 三端看到的是同一个进程。
  - 多机模式：platform 让每台机器主动外连，形成反向隧道，再用飞书登录统一入口。
  - platform 本身**只存元数据，不存消息**。
- **一句话**：**飞书遥控编码 Agent，platform 是多机入口**。

### Multica：任务看板，不涉及 IM 聚合

把编码 Agent 当队友，分配 issue、跟踪进度。我们只借鉴它 Linear 风格的界面，不借鉴产品定位。

### life-agent（本项目）：消息是数据，也是工作的起点

> 2026-11-19 修订：定位从"个人消息数据层"扩展为**个人工作平台**，即 IM 信息入口加上多 Agent 协同。Agent 这一侧的设计见 [design-agents.md](design-agents.md)。
>
> 这样一来，Persona、botmux、Multica 在 Agent 这部分就成了直接竞品。我们和它们的区别不在"要不要 Agent"，而在**任务从哪来、Agent 能拿到什么上下文**：
> - 它们的任务来自对话、话题或 issue；
> - 我们的任务来自全渠道消息，并且带着完整的消息历史和全文搜索作为上下文。

- **要解决的问题（信息入口部分）**：你的消息散落在微信、飞书、QQ 和多个邮箱里。我们把它们**原样收进一个地方**，回答三个问题：
  - 现在哪些消息需要我处理？
  - 线上告警有哪些？
  - 某件事当时在哪个群、谁说的？
- **消息的角色**：消息是**一等数据**。hub 存全部正文，统一去重、计未读、分拣、全文搜索。
- **多端**：一个 hub 作为唯一的数据中心，各设备都只是访问它的窗口，跨设备用 Tailscale。
- **一句话**：**消息驱动的个人工作平台**。信息入口是全渠道收件箱、分拣和搜索；干活靠多 Agent 协同，驱动现成的 CLI，跑在各台机器的 node 上。

### 定位坐标

```
                      存 IM 正文（消息是数据）
                              ▲
                              │   ● life-agent
                              │
  面向人与人的消息 ◄───────────┼───────────► 面向人与 AI 的对话
                              │
           ● botmux（正文留在飞书）    ● OpenClaw（存的是 AI 对话）
                              │      ● Persona（Inbox 只存摘要）
                              ▼
                     不存 IM 正文（消息是指令或信号）
```

## 2. 能力对比

### 2.1 渠道覆盖：能不能读到你的聊天记录

| 渠道 | OpenClaw | Persona | botmux | life-agent |
|---|---|---|---|---|
| 微信聊天记录 | ✗ | ✗（iLink bot 通道） | ✗ | ✅ 读解密后的本地库（只读） |
| QQ | ✗ | ✗ | ✗ | ✅ NapCat / OneBot v11 |
| 飞书（本人身份） | ✗ | 私有扩展 | 机器人身份 | ✅ lark-cli 以用户身份同步 |
| 邮件 | — | ✅ IMAP，每 3 分钟 | ✗ | ✅ IMAP，多账号，按线程归并 |
| iMessage | ✅ 经 SSH 调用 Mac 上的 imsg | ✅ Mac node 读 chat.db | ✗ | 计划中 |
| WhatsApp / Telegram / Discord | ✅ | Telegram ✅ | ✗ | 计划中（Telegram） |

### 2.2 多端同步

| 能力 | OpenClaw | Persona | botmux / platform | life-agent（v1 后） |
|---|---|---|---|---|
| 谁连谁 | 设备主动连 Gateway WS（18789） | 设备连 hub；电脑主动外连 WSS | 机器主动外连 platform；每个数据流单独一条 WS | 设备经 Tailscale Serve 连 hub |
| 实时推送 | 推内容本身（先落库，再回读推送） | 多数只推"有变化"提示，客户端再拉 | 飞书卡片按 1.5～2s 节流刷新 | 只推"有变化"提示，客户端再拉 |
| 断线后补齐 | 三层：连接级序号、会话级增量游标（追不上就重置）、在线状态版本号 | 事件序号可续传，但日志只在内存保留 15 分钟 | **没有**，只靠快照覆盖 | 全局变更序号 + SSE `Last-Event-ID` 自动续传 |
| 重复提交 | 幂等键持久化 | 操作 ID 回执保留 30 天 | 飞书事件双层去重 | 消息按 ID 去重；发送带幂等键 |
| 两端同时操作 | 每个会话同一时刻只跑一轮，FIFO 排队 | 先到者生效，后到者返回 `already` | 审批先点者赢；终端输入无锁 | 已处理带版本校验：有新消息就不清除 |
| 已读 / 已处理 | 存服务端，用 CAS 写入 | 存服务端，不回写源平台 | — | 存服务端，不回写源平台 |
| 客户端离线缓存 | IndexedDB；iOS SQLite + 离线发件箱 | iOS 缓存两轮；Android SQLite | 无 | 计划中 |
| 手机推送 | APNs 中继只推唤醒信号；不推给正在看的设备 | APNs 中继 + HPKE 加密 | 飞书本身 | 计划中（Web Push，只在变成"需要看"时推） |
| 多 hub | 不支持联邦 | 组网后只有主 hub 接受 App 凭据 | platform 多副本靠 Redis 路由 | 单一 hub；远程渠道通过 agent 模式接入（计划中） |

### 2.3 重量

| | OpenClaw | Persona | botmux + platform | life-agent |
|---|---|---|---|---|
| 规模 | 约 6.5 万文件 | 约 1.2 万文件 | 约 6.6k + 270 文件 | 约 3.6k 行 Go + 1.3k 行前端 |
| 依赖 | Node，SQLite / Postgres | Go，Postgres，五端原生客户端 | Node；platform 需要 MongoDB、泛域名和 TLS | 单一 Go 二进制 + SQLite |
| 部署 | 一个 Gateway 进程 | hub + manager + 各电脑上的 node | 每台机器一个 daemon，外加 platform | 常开 Mac 上一个进程 |

## 3. 决策

1. **自建，不基于 Persona、OpenClaw 或 Multica。**
   - 我们也要做 Agent 协同，但它们都缺我们的核心：完整的全渠道消息数据，尤其是微信和 QQ 的聊天记录。
   - 它们的 Agent 侧也都比我们需要的重：Persona 是五端原生客户端加 Postgres；Multica 是 Postgres 加多端，而且是 source-available 许可，不能直接复用代码。
   - 以扩展形式寄生在它们上面，要背上全部的重量和迭代节奏。
   - Agent 运行时只做轻量适配，驱动现成的 CLI，不造运行时。
2. **hub 模式是刚需**：微信、QQ、邮件、飞书的连接器都必须 7×24 跑在固定位置；已读和已处理状态只能有一份；推送需要一个常驻的判断者。
3. **借鉴设计，不引入框架。** 采纳的做法见下一节。

## 4. 借鉴清单

| # | 做法 | 来源 | 状态 |
|---|---|---|---|
| 1 | "已处理"带版本校验：客户端看过之后又来了新消息，就不清除"需要看" | Persona bulk 的 version / as_of；OpenClaw 已读写入的 CAS | ✅ v1 |
| 2 | 全局变更序号，作为 SSE 事件 ID；重连自动补发，差太多就整体重拉 | OpenClaw 增量游标；Persona 事件序号 | ✅ v1 |
| 3 | 发送带幂等键，用回执表重放第一次的结果 | Persona `X-Persona-Op-Id`；OpenClaw 持久化幂等键 | ✅ v1（发送、建任务） |
| 4 | 默认只监听本机，跨设备用 Tailscale Serve；身份头先用 `tailscale whois` 反查验证 | OpenClaw | ✅ v1 |
| 5 | 慢客户端宁可断开，也不静默丢事件 | OpenClaw / Persona | ✅ v1（SSE 队列满时断开） |
| 6 | 推送只在变成"需要看"时发；正在看的设备不推 | OpenClaw Web Push | 计划中 |
| 7 | node 模式：远程机器主动外连，心跳发现假死，指数退避，重复连接踢掉旧的（4001）；补上 seq/ack 和持久化 outbox | botmux 隧道客户端 | ✅ 已实现 |
| 10 | 驱动现成的 Agent CLI，每个 CLI 一个适配器，自动发现 | Multica daemon、botmux | ✅ Claude Code、Codex（只参考思路，不复用代码：Multica 有附加许可条件） |
| 11 | 审批：Claude Code 的 PreToolUse hook 阻塞等人回答，多端先到者生效 | Persona、botmux | ✅ 已实现；超时改为自动拒绝（无人值守时更安全） |
| 12 | 主 Agent 拆解任务，派给多个 node 并行，再汇总结果 | Persona 主 Agent、botmux lead bot | 设计中 |
| 8 | 收件箱分三档：需要你处理 / 知悉 / 噪音；规则优先级为 sender > chat | Persona Inbox | 计划中（LLM 分拣） |
| 9 | 客户端本地快照：先显示缓存，再刷新 | OpenClaw Web IndexedDB | 计划中 |

明确**不采纳**的：
- Persona 只在内存保留 15 分钟的事件日志：我们的数据本来就落库，用游标更可靠。
- botmux 每个流单独建一条隧道连接。
- 设备签名配对：在单用户 + Tailscale 访问控制下，收益小于成本。

## 5. 参考资料

- OpenClaw 文档：[Architecture](https://docs.openclaw.ai/concepts/architecture)、[Remote access](https://docs.openclaw.ai/gateway/remote)、[Tailscale](https://docs.openclaw.ai/gateway/tailscale)
- Persona：[README](https://github.com/daidr/persona)；源码中的 `docs/server/architecture.md`、`docs/protocol/protocol.md`、`docs/assistant/inbox.md`、`docs/operations/network.md`
- botmux：[文档站](https://deepcoldy.github.io/botmux/)；platform 的 [v0.1.0 release](https://github.com/botmux-ai/botmux-platform/releases/tag/v0.1.0)
- 微信接入现状：`sjzar/chatlog` 已于 2025-10 收到官方函件后删除代码，详见 [docs/connectors.md](connectors.md)
