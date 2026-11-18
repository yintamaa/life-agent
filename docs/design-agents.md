# 设计：多 Agent 协同与工作能力

> 状态：**草案，待确认**。末尾的"待决问题"需要拍板后再实现。

## 1. 目标

life-agent 是**个人工作平台**，由两块能力组成：

- **信息入口**：已有。把微信、飞书、QQ、邮件收进一个收件箱，分拣出"需要看"和"线上告警"。
- **干活的能力**：本文。把事情交给 Agent 去做。Agent 跑在你的各台机器上，带着你的 skills，进度和审批在所有设备上实时可见。

两块能力在一个闭环里串起来，这也是我们和竞品的根本区别：

```
  IM 消息 ──► 分拣 ──► 任务 ──► 派给 Agent（某台机器、某个工作目录、某套 skills）
     ▲                                  │ 实时进度 / 审批请求（手机、电脑同步）
     └──────── 起草回复，你确认后发回原会话 ◄──┘
```

几个典型场景：

| 场景 | 入口 | Agent 做什么 | 回到哪里 |
|---|---|---|---|
| 线上告警 | 告警栏里的机器人卡片 | 跑诊断 skill（如 `alarm-auto-diagnosis`、`grafana-dashboard-inspection`），产出诊断结论 | 你确认后回复到告警群 |
| 有人 @你提需求 | 「需要看」里的一条消息 | 读上下文（同一会话的历史、全文搜索的相关讨论），在对应仓库里改代码或写方案 | 起草回复，附上 MR 或文档链接 |
| 你自己发起 | 任务看板，或直接和主 Agent 对话 | 拆解成子任务，分派到不同机器上并行执行 | 看板和消息里同步 |

## 2. 设计原则

1. **不造 Agent 运行时。** 直接驱动你已经装好、登录好的 CLI：Claude Code、Codex 等。这和 Multica、botmux 的做法一致。
2. **hub 只负责编排，执行留在节点上。** 代码、凭据和 CLI 都留在执行它的机器上。公司代码只在工作电脑上跑，hub 上只有任务元数据和事件流。
3. **节点是统一概念。** 每台机器上跑同一个二进制的 `node` 模式，同时承载连接器（这台机器上的 IM，比如工作电脑上的飞书）和 Agent 运行时（这台机器上的 CLI 与代码仓库）。上一轮规划的"agent 模式"并入这里。
4. **多端同步复用 v1 的机制**：变更序号、断线补齐、操作幂等、先到者生效。Agent 会话的事件流和收件箱走同一套。
5. **默认需要你审批。** 有副作用的操作（发消息、写远程、执行危险命令）都要求批准；放权要显式配置。

## 3. 架构

```
                        ┌───────────── hub（常开 Mac）──────────────┐
  手机 / 电脑 ── Tailscale ─►  收件箱 · 任务 · 会话事件流 · 审批 · 调度  │
                        └────────────▲───────────────▲─────────────┘
                     node 主动外连 WS │               │
              ┌──────────────────────┴──┐   ┌────────┴─────────────────┐
              │ node: 工作电脑            │   │ node: 常开 Mac（与 hub 同机） │
              │ · 连接器：飞书             │   │ · 连接器：微信、邮件、QQ      │
              │ · 运行时：Claude Code、Codex│   │ · 运行时：Claude Code        │
              │ · 仓库：公司代码 (worktree) │   │ · 仓库：个人项目             │
              └─────────────────────────┘   └──────────────────────────┘
```

### 3.1 node ↔ hub 协议（借鉴 botmux 隧道和 OpenClaw 帧格式）

- **连接**：node 主动拨 hub 的 `wss://…/api/node`，用 Bearer token 认证（v1 已有 `auth.tokens`）。
  - 重连：并行拨号、30 秒心跳发现假死连接、指数退避。
  - 同一个 node 重复连上时，踢掉旧连接（WS 关闭码 4001）。
- **帧格式**：JSON 文本帧，分三类：
  - `req` / `res`：hub 调用 node，比如启动会话、发送输入、取消、发 IM 消息。
  - `event`：node 上报，比如会话输出、审批请求、IM 消息入库。
  - 有副作用的请求都带幂等键。
- **上报可靠性**：node 本地维护一个 outbox（SQLite），事件按序号递增，hub 确认（ACK）后再删除。断线重连后从上次确认的位置续传，补上 botmux 缺的 seq/ack。
- **连接器复用**：现有的 `connector.Sink` 接口加一个"远程实现"，把数据经 WS 上报给 hub。连接器代码不用改。

### 3.2 运行时适配

```go
type Runtime interface {
    Name() string                           // "claude-code" | "codex" | ...
    Detect(ctx) (version string, ok bool)   // 在 PATH 里自动发现
    Start(ctx, Spec) (Session, error)       // Spec: 工作目录、prompt、skills、权限模式
}
type Session interface {
    Events() <-chan Event   // 文本增量、工具调用、审批请求、用量、结束
    Send(input string) error
    Cancel() error
}
```

- **Claude Code**：以无交互模式运行，输出为流式 JSON；审批通过 PreToolUse hook 回调 node，node 再转给 hub，推送到手机。实现前要对照官方文档，确认参数和 hook 协议。
- **Codex**：以 `codex exec` 的 JSON 输出模式运行。
- **工作区隔离**：每个任务一个 git worktree，参照你常用的 `wt-*` 目录习惯，避免并行任务互相踩踏。
- **skills**：直接复用 `~/project/skill-hub/skills/`。节点上的 CLI 本来就加载这些 skills，任务里只需要指明用哪个。

### 3.3 数据模型（hub）

| 表 | 关键字段 |
|---|---|
| `tasks` | id、title、status（todo/running/waiting_approval/done/failed）、source_conversation_id、source_message_id、node、runtime、workdir、change_seq |
| `agent_sessions` | id、task_id、node、runtime、native_session_id、status、started_at、usage |
| `session_events` | (session_id, seq) 主键、type、payload；一律先落库再推送 |
| `approvals` | id、session_id、request、status、decided_by、decided_at；先到者生效，后到者返回 `already` |
| `nodes` | id、name、capabilities（连接器和运行时清单）、last_seen、version |

`tasks` 也就是侧栏的「待办」模块。和收件箱一样带变更序号，所以多端同步机制直接复用。

### 3.4 调度的分阶段

1. **单 Agent**：一个任务由一个会话完成，你手动选机器和运行时，或者按规则自动分配。比如"仓库 X 的任务派给工作电脑"。
2. **规则触发**：消息命中规则时自动建任务，比如告警栏的某类卡片自动触发诊断 skill。默认只产出结论，不做任何写操作。
3. **主 Agent 拆解**：一个主 Agent 读取任务，拆成子任务分派给不同 node 并行执行，再汇总结果。借鉴 Persona 的主 Agent 和 botmux 的 lead bot。

## 4. 和竞品的关系（Agent 这一侧）

| | Persona | botmux | Multica | life-agent |
|---|---|---|---|---|
| 任务从哪来 | 和主 Agent 对话 | 飞书消息（一个话题一个 CLI 会话） | issue 看板 | **全渠道消息**、看板、主 Agent |
| Agent 拿得到的上下文 | Inbox 摘要、连接器 | 当前话题 | issue 内容 | **完整的 IM 历史和全文搜索** |
| 结果回到哪 | Persona 客户端 | 飞书卡片 | issue | **原会话**（起草后你确认）+ 看板 |
| 重量 | 重 | 中 | 中重（Postgres，多端） | 轻（单一二进制 + SQLite） |

我们的切入点是**消息驱动的工作**：任务带着完整的消息上下文进来，做完再回到消息里去。

## 5. 待决问题

1. **先支持哪些运行时**：只做 Claude Code，还是同时做 Codex？
2. **Agent 跑在哪**：只在常开 Mac 上跑，还是一开始就要 node 模式，让公司代码留在工作电脑上？
3. **第一个入口**：告警自动诊断、把 @我的消息转成任务、任务看板、和主 Agent 对话，先做哪个？
4. **放权程度**：Agent 只起草不执行、执行但每步审批、白名单内自动执行，选哪一档？
