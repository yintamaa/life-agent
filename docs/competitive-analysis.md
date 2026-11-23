# 竞品分析：多端 IM 协同与个人 hub

> 调研时间：2026-11-18，HappyClaw 于 2026-11-21 补充。调研方式：拉取源码只读分析，关键结论都附了出处文件。没有运行任何竞品代码。
>
> | 项目 | 分析版本 |
> |---|---|
> | [OpenClaw](https://github.com/openclaw/openclaw) | `6b5bc83ae` |
> | [Persona](https://github.com/nwverse/persona) | `9b5038a67`（2026-11-23 复核；此前误用停更镜像 daidr/persona 的 `4aaef55`，见第 7 节） |
> | [botmux](https://github.com/deepcoldy/botmux) | `697de2f` |
> | [botmux-platform](https://github.com/botmux-ai/botmux-platform) | `bd7faf3`（v0.1.0） |
> | [HappyClaw](https://github.com/riba2534/happyclaw) | `d90e9d9`（2026-11-20） |
> | [Multica](https://github.com/multica-ai/multica) | 仅作界面参考 |
| [herdr](https://github.com/herdrdev/herdr) | `e8d7d97`（v0.9.5，2026-11-23 本机安装实测；本文中唯一实际运行过的项目） |

## 1. 定位：大家在解决不同的问题

这几个项目常被放在一起讨论，因为它们都有"hub""多端""接飞书 / 微信"这些关键词。但如果问**"消息"在系统里扮演什么角色**，它们的差别就很清楚了。

### OpenClaw：IM 是 AI 助手的"嘴和耳朵"（2026-11-23 起不再跟踪，内容为历史参考）

- **要解决的问题**：让一个 AI 助手同时出现在你的 WhatsApp、Telegram、iMessage、Discord 等渠道里，任何渠道发来的私聊都汇入同一个主会话，助手在原渠道回复。
- **消息的角色**：消息是**和 AI 对话的输入输出**。Gateway 存的是"人与助手"的对话记录（每个 agent 一个 SQLite，事件按序号存储），而不是你和朋友之间的聊天记录。
- **多端**：手机、Mac、网页都是 Gateway 的客户端；手机还能作为 node，向助手提供拍照、定位、录屏等能力。
- **一句话**：**个人 AI 助手的网关**。

### Persona：面向所有人的 SuperAgent，正在补齐"生活"

> 2026-11-23 按最新上游 `9b5038a67` 重写。11-09 以来又有 5,500 多个提交，期间往"生活"方向大幅扩展。

- **要解决的问题**：标语仍是 "One SuperAgent for everything you do"。
  - 主 Agent 拉取上下文、拆解任务，派给各台电脑上的 Claude Code、Codex、opencode 等，手机上看进度、做审批。
  - 新增超级规则 0「Everyone-first」，从开发者工具转向**普通人**。
  - 新增北极星指标：**每天打扰你几次**（越少越好），以及主会话的可用率。
- **消息的角色**：
  - **Inbox**：收集需要你处理的事，由小模型分成"需要你处理 / 知悉 / 噪音"三档。条目只存分诊上下文，即最近 20 条消息或邮件的前 3000 字。来源是飞书、IMAP 多账号、GitHub、iMessage、Telegram、X（仅 @提及）。
    - **新增 iOS 快捷指令「Handle Notification」**：任意通知或短信都能推进来。
    - **源端已处理就自动归档**：依据是邮件 `\Answered`、你本人回复、或待办完成。
  - **知识库存全文（纠正此前的结论）**：
    - 聊天按天写成 `chats/<id>/messages/YYYY-MM-DD.md`，保留 365 天；
    - 来源包括飞书网关、邮件、iMessage、Telegram、Discord；
    - 回填历史需要你审批，有持久化搜索索引；
    - 设计思路是"文件为主、数据库只做索引"。
  - **飞书**：
    - lark-cli 以本人身份读取 @我、私聊、你参与的话题，进入 Inbox，但**不进知识库**；
    - 全量浏览和归档依赖私有网关扩展，即"第三方用户身份的 web 客户端"。
  - **微信**：仍然只是 iLink bot 通道，改成了公开扩展，**读不到你的微信聊天记录**。
  - **没有**：QQ、WhatsApp、Slack。短信只能在 Mac 上读 `chat.db`。
- **生活能力（新增大量内容）**：
  - **健康**：读手机里的步数、能量、营养、心率、睡眠、体重，最多 90 天，还有体能训练。iOS 运动后自动出报告。体检 PDF 逐项做跨年对比，有独立的 Health 空间。
  - **位置**：只做按需定位、附近地点、路线；按地点提醒交给系统围栏。**不保存轨迹，没有足迹。**
  - **日历、通讯录**：可读可写，写操作逐条在手机确认。提醒能导入为待办。
  - **智能家居**：HomeKit；另有 Home Assistant 扩展。
  - **简报与回顾**：工作日 09:00 早报（待办、日程、待回消息、提醒）。Watch 全天录音、手机本地转写，晚上生成每日回顾。
  - **没有**：消费、账单、快递、出行、天气、生活订阅。
  - **敏感信息分类**：health、finance、relationships、emotion、legal_id 按场景限制外发。
- **多端**：
  - 客户端：iOS、iPad、Watch、Android、HarmonyOS、CarPlay、Web、Electron 桌面（原生 Mac 冻结）。
  - 推送：APNs 中继（HPKE 加密），新增 VoIP"紧急来电"和 Web Push。
  - 同步：断线续传窗口不变；新增多 hub 分组和 Rust 共享同步核心。
- **审批与编排**：PreToolUse 先答者胜；审批卡加"有疑问"按钮；常驻授权按目录、Git 身份和动作（merge、push、deploy、send、spend）限定范围；同一答案出现 3 次就建议生成规则；leader 会话、A2A。
- **代价**：极重（多端原生、Postgres、hub 与 manager 双进程），迭代极快（每天 300～470 个提交）。
- **一句话**：**面向普通人的多端 SuperAgent，工作收件箱加正在补齐的生活能力（健康、日历、家居、回顾）**。

#### 评价：读官方介绍文档后（2026-11-23）

> 来源：飞书文档《Persona：一个 SuperAgent，搞定你的所有事》（revision 16）。

文档里新增的技术细节：
- **主聊天就是一个 headless Claude Code**：跑在 hub 机器上，stream-json 常驻；空闲 20 分钟回收，下一条消息用 `--resume` 恢复，新聊天直接用预热好的备用进程。可以换成 codex app-server 或 Agent SDK。
- **按成本分三档**：即时操作直接调工具；只读任务自动开始；要改代码或第一次进某个项目时，先出计划卡等用户点 Start。主聊天可以代答 Agent 的普通提问，权限请求和计划审批不代批。向用户的提问最多挂 2 小时。
- **herdr**：能识别 Agent 的终端复用器。node 通过它的 socket API 读屏、发键，不改 Agent 本身；Agent 停下等人时 0～0.35 秒推到手机。你在电脑前看到的就是手机上那个会话。
- **知识库不用向量库**：靠 rg 做多关键词逐行匹配，42 万行、25 MB 搜一次约 150 ms，代价是没有语义召回。身份线索先做 HMAC 再存。
- **扩展**：约 26 个扩展点，清单里有未知字段就拒装；Linux 用 bwrap + systemd-run 做沙箱（默认 1 GB 内存、1 个核，看不到 home），macOS 用 sandbox-exec；扩展拿不到模型 key，由 manager 代调；官方目录用 ed25519 签名。
- **工程**：约 58% 的提交带 Claude 共同作者署名；三端读同一批 JSON 夹具，保证行为一致。

判断：
1. **"面向普通人"和真实门槛对不上。** 用起来要一台常开的机器、Docker、`curl | sh`、iOS 26 TestFlight，外加 Claude Code 订阅。文档里"一天里怎么用"的五个场景全是程序员的一天（兼容旧接口、审 PR、dev server、批计划）。普通人的故事在文档里一个也没有。实际用户是手上有多台机器的重度开发者。
2. **铺得太宽。** 从手机到 Agent 要经过 hub、manager、node、herdr 四层，客户端覆盖 Vision Pro、CarPlay 和 Watch，扩展点 26 个。这么大的面主要靠 Agent 每天几百个提交撑着，深度和稳定性存疑。文档没给任何可靠性数据，也没提北极星指标"打扰次数"的实测值。
3. **"共享上下文"说得比做到的多。** 飞书走 lark-cli 不进知识库，全量归档要装私有网关，即第三方用户身份的 web 客户端。在公司环境下，这等于把公司数据落到个人 NAS 上，是合规风险。微信聊天记录、QQ 都没有，对中文用户来说"搞定你的所有事"缺了最大的一块。
4. **秘书模式有两个隐患。** 一是秘书和 N 个 Agent 共用同一份订阅额度。二是代答技术问题容易把错误自信地传下去，"普通提问"和"需要拍板"的边界又是模型自己判断的。
5. **做得对、值得抄的地方**：hub 只管通信、业务全放 manager；知识库文件为主，用 rg 检索；Inbox 拿不准就算需要处理，模型不可用时有规则兜底；对外动作必须确认；扩展拿不到 key；跨端共享夹具。

对我们的意义：差异点不变，仍然是微信/QQ 全文、短信和邮件里的生活事务、单二进制。可以直接借鉴的有：三档成本、"提问 2 小时超时，用户一开口就撤回"、扩展拿不到 key。

和我们的关系：Persona 是**最主要的竞品**，"生活"方向开始正面重合。我们仍然明显不同的地方：

1. **微信全文**（解密本地库）和 **QQ**（NapCat）：Persona 完全没有。
2. **飞书以本人身份全量同步并可搜索**：Persona 的 lark-cli 路径不归档，全量要靠私有网关。
3. **从短信和邮件识别快递取件码、银行动账、账单、行程、订阅**：Persona 没有。
4. **轻**：单一二进制加 SQLite，对比它的多端原生加 Postgres。

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
- **上手体验**（2026-11-23，用户亲自试用）：感觉"重"，dashboard 太复杂。对比同一天试的 herdr：一个二进制、一个终端界面，状态一眼能看清。我们的 Web 端要引以为戒：首页只回答"哪些要我处理、哪个 Agent 在等我"，其余功能收到二级页面里。

### HappyClaw：IM 是 Claude Code 工作台的"前门"

- **要解决的问题**：把 Claude Code 运行时包装成一个自托管、常驻在线的**多用户**服务。你可以从网页（PWA）或 8 种 IM 机器人使用同一套智能体、工作区、Skills 和定时任务。它默认面向多人，内置计费和兑换码，也有单人模式。
- **消息的角色**：消息是**发给智能体的指令**。8 个渠道全部以 **Bot 身份**接入，未绑定的会话保持静默：
  - 微信走官方 iLink Bot，只能和 Bot 一对一私聊，没有群聊；
  - QQ 走官方 QQ Bot API，只收私聊 Bot 和群里 @Bot 的消息；
  - 飞书用自建应用的租户 token；
  - WhatsApp 虽然扫码登录个人号，但代码主动丢弃了历史同步消息和未配对的会话。

  它存的是**已绑定会话里你和智能体的对话全文**，聊天消息没有全文搜索（FTS 只建在 Workspace Memory 上）。
- **Agent 侧**：
  - 运行时是 Claude Agent SDK，`permissionMode: bypassPermissions`，**没有逐次工具审批**。唯一的人工审批是团队模式里把 work item 从只读放开为可写。
  - 执行环境：宿主机，或 Docker 沙箱（普通成员强制使用 Docker）；宿主机工作区是 git 仓库时，每个会话一个 git worktree。
  - 团队模式下，一个 router 会话负责路由，每个任务开一个 worker 会话。
  - **没有远程节点**，只能单机执行。
  - Codex 不是运行时，而是一个把 ChatGPT 订阅转成 Anthropic 接口的网关。
- **代价**：TypeScript / Node 单体，约 2100 个文件、非测试代码约 37 万行，迭代极快（两个月约 180 个 PR）。多用户、ACL、计费这一整套能力，对个人用户来说用不上。
- **一句话**：**多用户的 Claude Code 工作台，Web 和 8 个 IM 机器人都是它的前门**。

它和我们的重合度是几个项目里最高的：都接了微信、QQ、飞书，都把 Claude Code 当执行引擎，都有任务和 worktree。但两边对消息的立场正好相反：

- **HappyClaw**：别人 @你的 Bot，Bot 帮你干活；
- **life-agent**：你和别人的全部聊天本身就是数据，从里面挑出要干的活，派给 Agent。

### Multica：任务看板，不涉及 IM 聚合

把编码 Agent 当队友，分配 issue、跟踪进度。我们只借鉴它 Linear 风格的界面，不借鉴产品定位。

### herdr：编码 Agent 的终端运行时（不是竞品，是可选的底座）

> 2026-11-23 用官方安装脚本装到 `~/.local/bin`（macOS 14 上 Homebrew 已不提供 bottle，源码编译又报错），在独立的 `probe` session 里无头实测，测完已删除。

- **要解决的问题**：在一个终端里同时管很多个编码 Agent。
  - 布局和 tmux 类似，分 workspace、tab、pane 三层；server 常驻后台，关掉客户端或断开 SSH，Agent 照样在跑。
  - 可以把多台 SSH 机器放进同一个窗口。
  - Rust 单二进制，22 MB，Apache-2.0 许可。
- **核心能力**：认出 pane 里跑的是哪种 Agent（支持 24 种），并标出 working / blocked / idle / done。Agent 停下来等人时，马上能看到是哪一个。
- **实现方式是读屏。**
  - 每种 Agent 一份 TOML 规则，匹配 OSC 标题、底部几行的正则和提示框区域。
  - 规则从 `herdr.dev/agent-detection/` 单独下发，带版本号。Agent 的 UI 改版时只更新规则，不用发新版本程序。
  - `agent explain` 会给出命中的是哪条规则，以及作为证据的屏幕文本。
- **实测结果**：
  - Claude Code 启动时弹出目录信任框，`agent start` 立刻返回 `agent_not_ready`，状态为 `blocked`，判断正确。
  - `agent prompt --wait` 先观察到 working，再等到 idle 才返回，一次往返约 11 秒；`agent read` 能读回回复内容。
  - 停掉 server 再启动：workspace、pane 和 cwd 都恢复了，Claude 进程没有恢复，会话也没续上。要续会话，得先运行 `herdr integration install claude`，往 `~/.claude/settings.json` 装一个上报 session id 的 hook，恢复时再由 herdr 在 pane 里敲 `claude --resume <id>`。这一步要改全局配置，本次没装。
  - 程序对外只连两处：更新清单 `herdr.dev/latest.json` 和识别规则清单。源码里没有遥测。
- **只管自己开的 pane，接管不了已经在跑的 Agent**（用户实测发现，源码 `e8d7d97` 核实）：
  - 识别的三路信号都绑在 herdr 自己分配的 PTY 上：pane 子进程的前台进程组（`src/pane.rs` 的 `probe_foreground_process`）、这个 PTY 的屏幕缓冲区（`src/detect/`）、以及 hook 上报。herdr 不扫描本机进程，也没有 adopt、attach 外部进程之类的命令。
  - 装了 integration 也没用：Claude 的 hook 脚本开头就检查 `HERDR_ENV=1`、`HERDR_SOCKET_PATH`、`HERDR_PANE_ID`，缺一个就 `exit 0`（`src/integration/assets/claude/herdr-agent-state.sh`）。这些变量只有 herdr 在 pane 里起进程时才注入，所以在 iTerm 或 VS Code 里启动的 `claude` 发出的上报会被直接丢掉。
  - 想纳管，只能在 herdr 的 pane 里重新起：先退出原来的进程，再在 pane 里 `claude --resume <id>`，会话可以续上。正在跑的那一轮没法无损迁移，因为 PTY 的控制端换不了（reptyr 一类工具在 macOS 上不可用）。
  - 对我们的启发：想覆盖"用户自己在终端里开的 Agent"，靠接管 PTY 走不通，要换别的信号源。
- **改代码能做到哪一步**（2026-11-23 本机核实，macOS 14，SIP 开启，Claude Code 2.1.330）：
  - **发现和粗状态，现成就有。** Claude Code 自己维护一份本机会话登记：每个交互式进程写一份 `~/.claude/sessions/<pid>.json`，里面有 `sessionId`、`cwd`、`status`（实测见到 `busy` 和 `idle`）、`updatedAt`，还有一个 `messagingSocketPath`（`/tmp/cc-socks/<pid>.sock`）。不管在哪个终端里启动都会写，pid 还活着就说明会话还在。这是未公开的内部格式，随版本可能变，读的时候要容错。
  - **"在等我审批"要靠 hook。** 登记里没看到 blocked 这种状态，得在全局 `~/.claude/settings.json` 里装 `Notification`、`PreToolUse` hook，按 session id 上报，并且不能像 herdr 那样要求 `HERDR_PANE_ID`。
  - **往里发消息**：
    - Claude Code 跨会话互发消息（SendMessage）走的就是那个 socket，协议没公开，第三方去逆向容易被版本升级打断。
    - 退一步可以用终端自己的接口：tmux `send-keys`、iTerm2 的 API 或 AppleScript、Terminal.app 的 AppleScript。VS Code 内置终端没有这类接口。
    - 直接往 `/dev/ttysNNN` 写只会显示在屏幕上，不会变成输入。`TIOCSTI` 注入按键要求那个 tty 是调用方的控制终端，否则得有 root。
  - **原样接管做不到。** Linux 上 reptyr 靠 ptrace 往目标进程里注入系统调用来换 PTY，macOS 开着 SIP 时拿不到别的进程的 task port，这条路不通。
  - **可行的"一键接管"**：等登记里的 `status` 变成 `idle`，取出 `sessionId` 和 `cwd`，结束原进程，在 herdr 的 pane 里 `cd <cwd> && claude --resume <sessionId>`。herdr 的 `src/agent_resume.rs` 已经有生成 resume 命令的逻辑，改动量不大。代价是原来那个终端窗口里的进程会退出，而且只能在 idle 时做。
  - **改 herdr 的难点**：herdr 的数据模型假设每个 pane 都有自己的 PTY（`src/pane.rs` 的 `PaneRuntimeIo`），要显示"不归它管、只读"的外部会话，就得加一种新实体，牵扯 UI、socket API 和持久化。上游提交非常频繁，fork 维护成本高。这件事放在我们自己的 node 里做（读登记目录加全局 hook）更合适。
- **和我们的关系**：
  - 我们的 node 用 `claude -p --output-format stream-json` 和 `codex exec --json` 无头驱动 Agent，状态来自结构化事件，比读屏可靠，也不受 UI 改版影响。代价是人没法中途接手，想接着聊只能 `--resume` 另开一轮。
  - herdr 正好反过来：Agent 跑在真终端里，人随时可以 attach 进去接手，状态则靠读屏推断。
  - Persona 在 node 下面用的就是 herdr 这一层（见上文）。
- **一句话**：**靠读屏识别 Agent 状态的终端复用器**。适合人和 Agent 轮流使用同一个会话，不适合当无人值守的执行引擎。

### life-agent（本项目）：消息是数据，也是工作的起点

> 2026-11-23 再次修订：定位为**个人生活大一统的 Agent**，见 [product-life.md](product-life.md)。和 Persona 相比，更重要的差异是"生活"能力：数据源要多要全（消息、短信、账单、快递、行程、健康、智能家居），并且自动整理出生活场景。工作编排是其中一块，不是全部。
>
> 2026-11-19 修订：定位从"个人消息数据层"扩展为**个人工作平台**，即 IM 信息入口加上多 Agent 协同。Agent 这一侧的设计见 [design-agents.md](design-agents.md)。
>
> 这样一来，Persona、botmux、Multica、HappyClaw 在 Agent 这部分就成了直接竞品。我们和它们的区别不在"要不要 Agent"，而在**任务从哪来、Agent 能拿到什么上下文**：
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
                              │      ● HappyClaw（存 Bot 会话全文）
                              │      ● Persona（知识库按天存全文，Inbox 只存摘要）
                              ▼
                     不存 IM 正文（消息是指令或信号）
```

## 2. 能力对比

### 2.1 渠道覆盖：能不能读到你的聊天记录

| 渠道 | OpenClaw | Persona | botmux | HappyClaw | life-agent |
|---|---|---|---|---|---|
| 微信聊天记录 | ✗ | ✗（iLink bot 通道，已改为公开扩展） | ✗ | ✗（iLink Bot，只有和 Bot 的一对一私聊） | ✅ 读解密后的本地库（只读） |
| QQ | ✗ | ✗ | ✗ | ✗（官方 QQ Bot：只有私聊 Bot 和群里 @Bot 的消息） | ✅ NapCat / OneBot v11 |
| 飞书（本人身份） | ✗ | lark-cli 以本人身份读 @我、私聊、参与的话题（不归档）；全量靠私有网关 | 机器人身份 | 机器人身份（租户 token，可按需拉 Bot 所在群的历史） | ✅ lark-cli 以用户身份同步 |
| 邮件 | — | ✅ IMAP，每 3 分钟 | ✗ | ✗ | ✅ IMAP，多账号，按线程归并 |
| iMessage / 短信 | ✅ 经 SSH 调用 Mac 上的 imsg | ✅ Mac node 读 chat.db；iOS 用快捷指令把通知和短信推进来 | ✗ | ✗ | 计划中 |
| WhatsApp / Telegram / Discord | ✅ | Telegram ✅；Discord 进知识库（未进 Inbox） | ✗ | ✅ 都是 Bot 入口（另有钉钉、企业微信） | 计划中（Telegram） |
| 聊天消息全文搜索 | — | ✅ 知识库索引（按天 Markdown，保留 365 天） | — | ✗（FTS 只建在 Memory 上） | ✅ FTS5 trigram |

### 2.2 多端同步

| 能力 | OpenClaw | Persona | botmux / platform | HappyClaw | life-agent（v1 后） |
|---|---|---|---|---|---|
| 谁连谁 | 设备主动连 Gateway WS（18789） | 设备连 hub；电脑主动外连 WSS | 机器主动外连 platform；每个数据流单独一条 WS | 浏览器 REST + 单条 WS `/ws`，30 秒心跳；IM 走各家 Bot SDK | 设备经 Tailscale Serve 连 hub |
| 实时推送 | 推内容本身（先落库，再回读推送） | 多数只推"有变化"提示，客户端再拉 | 飞书卡片按 1.5～2s 节流刷新 | WS 推 `new_message`，带上和 REST 分页同一个 `ingest_sequence` | 只推"有变化"提示，客户端再拉 |
| 断线后补齐 | 三层：连接级序号、会话级增量游标（追不上就重置）、在线状态版本号 | 事件序号可续传，但日志只在内存保留 15 分钟 | **没有**，只靠快照覆盖 | `afterSequence` 增量拉取；重连时服务端推运行快照 | 全局变更序号 + SSE `Last-Event-ID` 自动续传 |
| 重复提交 | 幂等键持久化 | 操作 ID 回执保留 30 天 | 飞书事件双层去重 | Web 发送按 `clientMessageId` 幂等；飞书 inbox 唯一键持久化去重；其它渠道只有内存 LRU | 消息按 ID 去重；发送带幂等键 |
| 两端同时操作 | 每个会话同一时刻只跑一轮，FIFO 排队 | 先到者生效，后到者返回 `already` | 审批先点者赢；终端输入无锁 | 多渠道共享会话，但按来源分批执行，回复回到各自来源 | 已处理带版本校验：有新消息就不清除 |
| 已读 / 已处理 | 存服务端，用 CAS 写入 | 存服务端，不回写源平台 | — | 未找到 | 存服务端，不回写源平台 |
| 客户端离线缓存 | IndexedDB；iOS SQLite + 离线发件箱 | iOS 缓存两轮；Android SQLite | 无 | 未找到 | 计划中 |
| 手机推送 | APNs 中继只推唤醒信号；不推给正在看的设备 | APNs 中继 + HPKE 加密；VoIP 紧急来电；Web Push；Android 离线补发 30 分钟 / 50 条 | 飞书本身 | Web Push，只推网页发起的轮次，正在看的不推；IM 来源由 IM 自己通知 | 计划中（Web Push，只在变成"需要看"时推） |
| 多 hub | 不支持联邦 | 多 hub 分组；组网后只有主 hub 接受 App 凭据 | platform 多副本靠 Redis 路由 | 单机 | 单一 hub；远程机器以 node 身份主动外连（Agent 已实现，连接器计划中） |

### 2.3 重量

| | OpenClaw | Persona | botmux + platform | HappyClaw | life-agent |
|---|---|---|---|---|---|
| 规模 | 约 6.5 万文件 | 约 1.2 万文件 | 约 6.6k + 270 文件 | 约 2100 文件，非测试代码约 37 万行 | 约 7.9k 行 Go + 2.5k 行前端 |
| 依赖 | Node，SQLite / Postgres | Go，Postgres，五端原生客户端 | Node；platform 需要 MongoDB、泛域名和 TLS | Node 22 + Hono + better-sqlite3 + React 19，外加每个渠道的 SDK；可选 Docker | 单一 Go 二进制 + SQLite |
| 部署 | 一个 Gateway 进程 | hub + manager + 各电脑上的 node | 每台机器一个 daemon，外加 platform | 一个 Node 服务，可选 Docker 沙箱 | 常开 Mac 上一个 hub，其它机器各一个 node |
| 许可 | MIT | MIT | MIT | MIT | — |

### 2.4 Agent 协同

| | Persona | botmux | Multica | HappyClaw | life-agent |
|---|---|---|---|---|---|
| 运行时 | 驱动各电脑上的 Claude Code / Codex（herdr、Orca、Paseo） | 30+ 种编码 CLI，一个话题一个会话 | 驱动 26 种 Agent CLI | Claude Agent SDK；Codex 只作为模型网关 | Claude Code + Codex，各一个适配器 |
| 任务从哪来 | 和主 Agent 对话 | 飞书消息 | issue 看板 | Web / IM 里和 Bot 对话；定时任务；团队模式由 router 分派 | **全渠道消息**一键转任务（带最近的消息作为上下文） |
| 逐次工具审批 | ✅ PreToolUse hook，先答者胜；常驻授权按目录、Git 身份和动作限定；同一答案出现 3 次建议成规则 | 审批卡先点者赢 | — | ✗（bypassPermissions；只有 work item 只读→可写的粗粒度审批） | ✅ PreToolUse hook，推到所有设备，先到者生效，hook 失败一律拒绝 |
| 执行隔离 | 各电脑本地 | 各机器本地，tmux | 各机器 daemon | 宿主机，或 Docker 沙箱；每个会话一个 git worktree | 每个任务一个 git worktree；Codex 用 workspace-write 沙箱 |
| 跨机器执行 | ✅ 电脑主动外连 hub | ✅ platform 反向隧道 | ✅ daemon | ✗ 单机 | ✅ node 主动外连，持久 outbox |
| 多用户 | 单人 | 团队（飞书登录） | 团队 | **多用户**（ACL、TOTP/Passkey、计费） | 单人 |

### 2.5 生活能力

| 能力 | Persona（2026-11-23） | life-agent |
|---|---|---|
| 健康 | ✅ 读手机健康数据（最多 90 天），运动后自动报告，体检 PDF 跨年对比 | 规划（Apple Health 导出 / 推送） |
| 位置 / 足迹 | 按需定位、地点提醒；**不存轨迹** | 规划（足迹时间线） |
| 日历 / 通讯录 | ✅ 读写，写操作逐条确认 | 规划（CalDAV、macOS 日历、飞书日历） |
| 智能家居 | ✅ HomeKit；Home Assistant 扩展 | 规划（Home Assistant，告警进告警栏） |
| 每日简报 / 回顾 | ✅ 工作日早报；Watch 全天录音生成晚间回顾 | 规划（"今天"简报、日记草稿） |
| 快递取件码 | ✗ | 规划（短信抽取，优先） |
| 账单 / 记账 / 还款提醒 | ✗ | 规划（信用卡账单邮件、动账短信、支付账单导入） |
| 出行（航班、火车） | ✗ | 规划（短信、邮件抽取 → 日程） |
| 生活订阅续费 | ✗ | 规划（邮件收据） |
| 报销 | ✗ | 规划（发票邮件） |
| 投资 | 自选股、价格提醒（Markets 扩展） | 规划（复用 Longbridge / 行情 skills） |
| 敏感数据分级 | ✅ health / finance / relationships / emotion / legal_id 按场景限制外发 | 规划（验证码不落库；公司数据只存元数据） |

小结：Persona 的生活能力集中在**手机自带的数据**（健康、日历、通讯录、HomeKit），由原生 App 读取；我们的机会在**散落在短信和邮件里的生活事务**（快递、账单、行程、订阅、报销），以及微信和 QQ 里的生活社交。

## 3. 决策

1. **自建，不基于 Persona、OpenClaw、Multica 或 HappyClaw。**
   - 我们也要做 Agent 协同，但它们都缺我们的核心：完整的全渠道消息数据，尤其是微信和 QQ 的聊天记录。HappyClaw 虽然也接了微信和 QQ，但都是官方 Bot 通道，只能收发发给 Bot 的消息。
   - 它们的 Agent 侧也都比我们需要的重：
     - Persona 是多端原生客户端加 Postgres，每天几百个提交，跟随成本极高；
     - Multica 是 Postgres 加多端，而且是 source-available 许可，不能直接复用代码；
     - HappyClaw 是多用户的 Node 单体，我们用不上它的 ACL 和计费，而且它没有逐次审批，也不能跨机器执行。
   - 以扩展形式寄生在它们上面，要背上全部的重量和迭代节奏。
   - Agent 运行时只做轻量适配，驱动现成的 CLI，不造运行时。
2. **hub 模式是刚需**：微信、QQ、邮件、飞书的连接器都必须 7×24 跑在固定位置；已读和已处理状态只能有一份；推送需要一个常驻的判断者。
   - 2026-11-23 确认：**中心化 dashboard 是核心设计原则。** hub 必须理解业务（消息、任务、审批），不能只做 botmux-platform 那样的透明转发。原因是"跨机器、跨 IM 的汇总、计数、提醒、搜索、统一审批"都要求有一个地方看得懂内容。botmux 不需要这样做，是因为它让飞书充当了这个汇总界面，而我们的数据来自多个 IM，没有现成的 App 能替我们汇总。
   - 各台机器（node）只负责执行和接入，并且总是主动外连 hub。
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
| 17 | iOS 快捷指令把通知和短信推进 hub：按 app + 标题 + 正文 + 分钟算哈希去重，带离线队列 | Persona「Handle Notification」 | 计划中（iPhone 端拿短信生活数据的现成入口，和 Mac `chat.db` 互补） |
| 18 | 知识库"文件为主、数据库只做索引"：按天 Markdown、可配保留期、扩大回填范围要重新审批 | Persona 知识库 | 待评估（我们现在是 SQLite 为主） |
| 19 | 敏感信息分类（健康、财务、关系、情绪、证件）＋分场景外发策略：推送不带正文、发给别人逐条确认 | Persona | 计划中（生活数据接入前要先有） |
| 20 | 源端已处理自动归档：邮件 `\Answered`、本人回复、待办完成 | Persona Inbox | 部分已有（本人回复会清除"需要看"）；邮件 `\Answered` 待加 |
| 21 | 北极星"每天打扰几次"：统计推送和"需要看"的数量，越少越好 | Persona | 计划中（推送上线时一起做） |
| 22 | 常驻授权：按目录、Git 身份和动作（push、deploy、send）限定范围；同一答案出现 3 次建议成规则 | Persona | 计划中（减少逐步审批的打扰） |
| 23 | 按地点提醒交给系统围栏，不保存轨迹 | Persona | 参考（足迹功能要给"只提醒、不存轨迹"的选项） |
| 8 | 收件箱分三档：需要你处理 / 知悉 / 噪音；规则优先级为 sender > chat | Persona Inbox | 计划中（LLM 分拣） |
| 9 | 客户端本地快照：先显示缓存，再刷新 | OpenClaw Web IndexedDB | 计划中 |
| 13 | 入站可靠链：按 `(平台, 账号, 消息 ID)` 唯一键落库去重，加上按会话游标、有额度上限的断线补拉 | HappyClaw 飞书 inbox | 部分已有（消息 ID 去重 + 每会话游标）；补拉的额度上限待加 |
| 14 | 从消息生成任务的分级：发送者在不在名单 × 是否 @我，分成明确 / 推断 / 忽略三档；配 observe（只建议）/ auto（自动派）两档 | HappyClaw 团队模式准入表 | 计划中（规则触发任务时采用） |
| 15 | 粗粒度审批：任务先只读运行，审批通过后才放开写权限 | HappyClaw work item | 计划中（作为"告警诊断"这类自动任务的默认档） |
| 16 | QQ 被动回复额度（带入站 msg_id、5 分钟内、最多 4 次）与微信 `context_token` 的生命周期处理 | HappyClaw | 仅供参考：我们走 NapCat，不受官方 Bot 额度限制 |
| 24 | 可选的"交互式"任务：Agent 跑在 herdr 的 pane 里，node 通过 socket API 发 prompt、读状态；手机上看进度，回到电脑前直接 attach 接手 | herdr（Persona 的用法） | 待评估（和现有无头模式并存，不替换） |
| 25 | 识别规则与程序分开发版：Agent 的 UI 改版时只更新规则文件；每次判定都能说出命中的规则和证据 | herdr agent-detection | 仅供参考：我们用结构化输出，不读屏；做交互式任务时再用 |

明确**不采纳**的：
- Persona 只在内存保留 15 分钟的事件日志：我们的数据本来就落库，用游标更可靠。
- botmux 每个流单独建一条隧道连接。
- 设备签名配对：在单用户 + Tailscale 访问控制下，收益小于成本。

## 5. 中心节点与连接方式

### 5.1 谁有中心

几家都有中心，区别在**中心存什么、机器怎么连上来**。botmux 是例外：单机时它没有自己的中心，飞书本身充当了中心。

| | 有没有中心 | 中心存什么 | 机器 / 设备怎么连中心 | 中心挂了会怎样 |
|---|---|---|---|---|
| OpenClaw | 有：Gateway，一台主机一个 | 会话、消息路由、所有 IM 渠道连接 | 设备主动连 Gateway 的 WS（默认只监听本机 18789，跨设备走 Tailscale 或 SSH） | 全部不可用；不支持多个 Gateway 互联 |
| Persona | 有：hub（可多 hub 分组） | 设备、收件箱、Agent 会话元数据（Postgres）；知识库和 Agent 正文在节点的文件里 | 设备连 hub；电脑默认主动连 hub（也支持 hub 去连电脑，或走 SSH） | 手机端不可用；电脑上的 Agent 照常跑 |
| Multica | 有：服务端（Go + Postgres，可自托管，也可用官方云） | issue、任务、Agent 配置、运行记录 | 机器上的 daemon 登记本机有哪些 CLI，然后每 3 秒轮询领任务 | 派不了新任务 |
| botmux | 单机没有，飞书就是中心；多台机器时可加 platform | platform 只存机器、用户、团队元数据，不存消息 | daemon 通过反向隧道主动连 platform | 单机照常运行，只是失去远程入口 |
| HappyClaw | 有：Node 服务本身 | 会话、工作区、用户 | 没有远程机器，只能单机执行 | 全部不可用 |
| life-agent | 有：hub | 全部 IM 正文、任务、审批 | 设备经 Tailscale 访问；其它机器的 node 主动连 hub，事件写本地 outbox，断线后续传 | 手机端不可用；node 上的任务继续跑，事件留在本地等 hub 恢复 |
| herdr | 没有；由你面前的客户端把各台机器汇总起来 | 不存；每台机器的 herdr server 各管各的 pane 和 `session.json` | **反过来，客户端经 SSH 主动连进每台机器**，并把桥接进程接到对方 server 的 socket 上。只有选中的机器推送画面，其余只推 Agent 状态和通知 | 没有中心可挂；断掉的机器灰显并自动重连，其它机器不受影响 |

规律是：**中心调度、存状态，执行放在各台机器上，并且总是机器主动连中心。** 原因是执行机器（公司电脑、家里的 Mac）都在 NAT 或防火墙后面，中心连不进去。

herdr 是例外：它面向能 SSH 登录的开发机，所以敢让客户端主动连过去。这样就不需要中心、账号或中转，鉴权全部交给 OpenSSH。代价是客户端必须能直连每台机器，而且汇总只在开着的客户端里存在。客户端一关，没有任何地方在盯着"谁在等我"，也就没法推送到手机。

### 5.2 "反向"不特殊，特殊的是连接里传什么

Persona、Multica、botmux 和我们的 node，都是执行机器主动连中心，所以"反向"并不是 botmux 独有的。真正的区别在于连接里传的是什么：

- **Persona / Multica / life-agent：传业务指令，中心听得懂。** 例如"在 tos 仓库跑任务 t123""t123 要执行 git push，请审批"。中心知道有哪些任务、跑到哪一步，于是能在一个界面里汇总所有机器的任务。
- **botmux-platform：传原始网络流量，只管转发。** 相当于自建的 ngrok 或 Cloudflare Tunnel，外加飞书登录：
  - 每台机器自带网页 Dashboard（`localhost:7891`），platform 负责让你在外面也能打开它；
  - 每台机器一个子域名，`m-<id>` 是 Dashboard，`t-<id>` 是终端；
  - 打开时，platform 通过控制连接通知 daemon，daemon 再连一条数据连接，浏览器的请求按字节原样转发过去。

```
① 启动时：   机器 A 的 daemon ──主动连──► platform（控制连接，一直保持）
② 你打开 m-机器A.platform.com → platform 飞书登录校验 → 通过控制连接通知 A
③           机器 A ──再连一条数据连接──► platform
④ 浏览器请求 ⇄ platform 原样转发 ⇄ 数据连接 ⇄ 机器 A 的 localhost:7891
```

### 5.3 为什么我们的中心必须理解业务

设想晚上 11 点你躺在床上，用手机想知道三件事：一共几条要处理？有没有 Agent 在等审批？上周谁说过"bucket 限流"？

| | 只做转发的中心 | 理解业务的中心 |
|---|---|---|
| 一共几条要处理 | ✗ 要逐台打开各机器的页面，自己加起来 | ✅ 一个数字 |
| 有 Agent 在等审批 | ✗ 要逐台去看；管子不知道里面是审批，**没法推送提醒** | ✅ 审批一出现就推到手机 |
| 跨机器搜索 | ✗ 每台机器各搜一遍 | ✅ 一次搜完 |
| 某台机器休眠了 | ✗ 它的数据完全看不到 | ✅ 已同步的照样能看 |

"汇总、计数、提醒、搜索、统一审批"都要求有一个地方看得懂内容。botmux 不需要，是因为飞书替它做了汇总；而我们的数据来自多个 IM 和生活数据源，没有现成的 App 能替我们汇总。这就是第 3 节记录的"中心化 dashboard"原则。

## 6. 公司数据存在哪：各家怎么做

背景：hub 放在个人的常开 Mac 上，公司飞书的聊天正文要不要存到 hub？

| | 消息全文存在哪 | 中心存什么 | 更接近 |
|---|---|---|---|
| OpenClaw | Gateway 所在机器 | 你和 AI 助手的对话（每个 Agent 一个 SQLite）；iMessage 由 Gateway 经 SSH 到 Mac 上读取后汇总 | A，但只存和 AI 的对话 |
| Persona | 节点上的知识库文件（按天 Markdown，保留 365 天） | hub 的收件箱条目只存分诊上下文；**知识库存全文**，来源包括飞书网关、邮件、iMessage、Telegram、Discord，回填历史需要审批；lark-cli 路径不归档 | 收件箱是 B，知识库是 A（带审批） |
| botmux | 飞书本身 | 本机只存会话状态，platform 只存元数据；从不复制消息 | 比 B 更彻底：不复制，直接用飞书当界面 |
| Multica | 服务端 | issue、任务、运行记录；飞书和 Slack 只发通知 | 不涉及 |
| HappyClaw | 它自己的服务端 | 已绑定会话里你和 Bot 的对话全文；WhatsApp 个人号的历史被主动丢弃 | A，但只存和 Bot 的对话 |

观察：

1. **OpenClaw、HappyClaw 存的全文都是你主动发给 AI 的内容**，不碰你和别人之间的聊天。
2. **Persona 是分层做的**（2026-11-23 纠正）：
   - 收件箱只存分诊上下文，这层是 B；
   - 知识库按天存全文，这层是 A，但**回填历史要你审批**，扩大范围要重新审批；
   - 用 lark-cli 读飞书时只进收件箱、不归档；要全量归档，必须你主动装私有网关扩展。
   - 它对敏感信息还有分类和外发限制。
3. **botmux 合规上最干净**：数据不搬家，代价是只能围绕飞书来做。
4. **"把你和别人的全部聊天汇总到个人中心"只有 Persona 的知识库做过一部分，而且是"需要审批的归档"。** 这一点值得直接借鉴：公司数据的全量归档默认关闭，开启时显式确认并说明范围。

我们的两个方案：

| | 方案 A：全文存 hub | 方案 B：全文留工作电脑，hub 只存元数据（可选摘要） |
|---|---|---|
| dashboard 能看到"需要看"、@我、告警 | ✅ | ✅ |
| 工作电脑关机时能看飞书正文 | ✅ | ✗ |
| 搜飞书消息 | ✅ 随时 | 要等工作电脑在线，由 node 在本地搜 |
| 合规风险 | 较高 | 低 |
| 实现 | 简单 | 复杂 |

当前倾向是**按数据来源分级**（待最终确认）：
- 微信、QQ、个人邮件、Discord 等个人数据走 A，全量汇总；
- 公司飞书走 B，加可选摘要。摘要要把正文发给模型，做成开关。

这样 dashboard 依然是中心，只是公司正文不落在个人设备上。

## 7. 跟踪机制

几个竞品迭代极快：Persona 每天 300～470 个提交，OpenClaw 每天约 280 个。只分析一次快照，很快就会过时。

> 教训（2026-11-23）：最初分析的 `daidr/persona` 其实是一个 11-09 起就停更的镜像，真正的上游是 `nwverse/persona`。两周后复查时，已经落后 5,515 个提交，期间 Persona 大举进入了健康、位置、日历等"生活"领域。

- **清单**：`docs/research/competitors.json`，记录每个竞品**真正的上游**、上次复核的提交和日期。
- **检查**：`scripts/track-competitors.sh [名字]`。它只拉提交历史，不检出、也不运行竞品代码，然后报告：
  - 落后多少个提交、每天多少个；
  - 和我们相关方向的提交（Inbox、各 IM、生活数据、推送、同步、节点、审批）；
  - 落后超过 200 个提交时提示重新复核。
- **复核**：按本文的维度重新分析，更新对应章节，把清单里的 `reviewed_commit` / `reviewed_at` 改成当时分析的版本，一起提交。
- **节奏**：建议每周跑一次。
- **范围**（2026-11-23 起）：
  - **重点只跟踪 Persona**：它和我们的方向，即工作收件箱加上生活能力，重合度最高，迭代也最快。
  - **OpenClaw 不再跟踪**：方向已过时，文档里的分析保留作历史参考。
  - botmux、HappyClaw 落后较多时再看。

## 8. 参考资料

- OpenClaw 文档：[Architecture](https://docs.openclaw.ai/concepts/architecture)、[Remote access](https://docs.openclaw.ai/gateway/remote)、[Tailscale](https://docs.openclaw.ai/gateway/tailscale)
- Persona：[README](https://github.com/nwverse/persona)（真正的上游）；`docs/feature-map.md`、`docs/assistant/inbox.md`、`docs/assistant/knowledge.md`、`docs/assistant/health.md`、`docs/apple/phone-tools.md`、`docs/extensions/lark-migration.md`、`docs/assistant/unified-entry.md`；源码中的 `docs/server/architecture.md`、`docs/protocol/protocol.md`、`docs/assistant/inbox.md`、`docs/operations/network.md`
- botmux：[文档站](https://deepcoldy.github.io/botmux/)；platform 的 [v0.1.0 release](https://github.com/botmux-ai/botmux-platform/releases/tag/v0.1.0)
- HappyClaw：[README](https://github.com/riba2534/happyclaw)；源码中的 `docs/RUNTIME-ARCHITECTURE.md`、`docs/ACL-MATRIX.md`、`src/wechat.ts`、`src/qq.ts`、`src/feishu.ts`、`container/agent-runner/src/index.ts`
- herdr：[README](https://github.com/herdrdev/herdr)、[socket API](https://herdr.dev/docs/socket-api/)、`herdr --skill` 输出的 Agent 使用说明；源码中的 `src/agent_resume.rs`、`src/integration/`；本机规则文件 `~/.local/state/herdr/agent-detection/remote/claude.toml`
- 微信接入现状：`sjzar/chatlog` 已于 2025-10 收到官方函件后删除代码，详见 [docs/connectors.md](connectors.md)
