import type {
  AgentPreset,
  Approval, Conversation, Counts, CreateTaskReq, Message, NodeStatus, SearchHit, Source, Task, TaskEvent, TaskEventPayload, TaskEventType, View,
} from "./types";

const now = Date.now();
const ago = (min: number) => new Date(now - min * 60_000).toISOString();

export const mockSources: Source[] = [
  { id: "wechat", platform: "wechat", name: "微信", state: "ok", last_sync_at: ago(0.05), error: "" },
  { id: "feishu", platform: "feishu", name: "飞书", state: "ok", last_sync_at: ago(0.15), error: "" },
  { id: "qq", platform: "qq", name: "QQ", state: "connecting", last_sync_at: ago(3), error: "" },
  { id: "mail:personal", platform: "mail", name: "个人邮箱", state: "ok", last_sync_at: ago(0.8), error: "" },
  { id: "mail:work", platform: "mail", name: "工作邮箱", state: "error", last_sync_at: ago(42), error: "IMAP 登录失败：授权码已过期，请在配置中更新 password_env" },
];

type Seed = Omit<Conversation, "id" | "can_send"> & { msgs: Array<[string, boolean, string, number, Partial<Message>?]> };

const seeds: Seed[] = [
  {
    source: "feishu", platform: "feishu", kind: "group", title: "存储产品周会", avatar: "",
    last_message_at: ago(2), last_preview: "@你 周四评审前能把计量方案的风险项补一下吗", last_sender: "孟子楠",
    unread: 3, mentions: 1, needs_attention: true, is_alert: false,
    msgs: [
      ["田树刚", false, "本周议程已更新到文档，大家会前看一下", 95],
      ["我", true, "收到，我这边 AK 轮转的进度会前同步", 80],
      ["孟子楠", false, "计量异动那块客户又问了一次，能不能给个预计时间", 12],
      ["孟子楠", false, "@你 周四评审前能把计量方案的风险项补一下吗", 2, { mentions_me: true }],
    ],
  },
  {
    source: "wechat", platform: "wechat", kind: "p2p", title: "妈妈", avatar: "",
    last_message_at: ago(6), last_preview: "周末回来吃饭吗？你爸买了鱼", last_sender: "妈妈",
    unread: 2, mentions: 0, needs_attention: true, is_alert: false,
    msgs: [
      ["妈妈", false, "降温了，记得穿厚点", 60 * 26],
      ["我", true, "知道啦", 60 * 25],
      ["妈妈", false, "[图片]", 8, { type: "image" }],
      ["妈妈", false, "周末回来吃饭吗？你爸买了鱼", 6],
    ],
  },
  {
    source: "feishu", platform: "feishu", kind: "group", title: "火山TOS-计量异动告警", avatar: "",
    last_message_at: ago(4), last_preview: "[P1] cn-beijing 计量延迟 > 15min，已持续 3 次", last_sender: "告警机器人",
    unread: 12, mentions: 0, needs_attention: false, is_alert: true,
    msgs: [
      ["告警机器人", false, "[P2] cn-shanghai 计量写入失败率 0.8%", 48, { type: "card" }],
      ["告警机器人", false, "[恢复] cn-shanghai 计量写入失败率 0.1%", 30, { type: "card" }],
      ["告警机器人", false, "[P1] cn-beijing 计量延迟 > 15min，已持续 3 次", 4, { type: "card" }],
    ],
  },
  {
    source: "mail:personal", platform: "mail", kind: "mail", title: "招商银行信用卡", avatar: "",
    last_message_at: ago(38), last_preview: "您的 11 月账单已出，本期应还 ¥3,482.16", last_sender: "招商银行",
    unread: 1, mentions: 0, needs_attention: true, is_alert: false,
    msgs: [
      ["招商银行", false, "尊敬的客户，您 11 月账单已出，本期应还 ¥3,482.16，最后还款日 12 月 5 日。", 38, { type: "mail", subject: "【账单】招商银行信用卡 11 月电子账单" }],
    ],
  },
  {
    source: "wechat", platform: "wechat", kind: "group", title: "周末徒步小分队", avatar: "",
    last_message_at: ago(55), last_preview: "周六 7:30 西二旗地铁站集合，带好雨衣", last_sender: "阿杰",
    unread: 28, mentions: 0, needs_attention: false, is_alert: false,
    msgs: [
      ["小林", false, "这周去香山还是凤凰岭？", 180],
      ["阿杰", false, "凤凰岭吧，人少一点", 170],
      ["我", true, "+1 凤凰岭", 160],
      ["阿杰", false, "周六 7:30 西二旗地铁站集合，带好雨衣", 55],
    ],
  },
  {
    source: "qq", platform: "qq", kind: "group", title: "大学同学群", avatar: "",
    last_message_at: ago(120), last_preview: "@全体成员 十周年聚会定在 12 月 20 号", last_sender: "班长",
    unread: 64, mentions: 1, needs_attention: true, is_alert: false,
    msgs: [
      ["老王", false, "今年还聚吗", 600],
      ["班长", false, "@全体成员 十周年聚会定在 12 月 20 号", 120, { mentions_me: true }],
    ],
  },
  {
    source: "feishu", platform: "feishu", kind: "p2p", title: "许志鹏", avatar: "",
    last_message_at: ago(60 * 3), last_preview: "好的，那我先按方案 B 提 MR", last_sender: "我",
    unread: 0, mentions: 0, needs_attention: false, is_alert: false,
    msgs: [
      ["许志鹏", false, "Increment 那个限流阈值你倾向哪个方案？", 60 * 3 + 20],
      ["我", true, "B 吧，回滚成本低", 60 * 3 + 10],
      ["许志鹏", false, "好的，那我先按方案 B 提 MR", 60 * 3],
    ],
  },
  {
    source: "mail:work", platform: "mail", kind: "mail", title: "GitHub", avatar: "",
    last_message_at: ago(60 * 26), last_preview: "[life-agent] New sign-in from Chrome on macOS", last_sender: "GitHub",
    unread: 0, mentions: 0, needs_attention: false, is_alert: false,
    msgs: [
      ["GitHub", false, "We noticed a new sign-in to your account from Chrome on macOS.", 60 * 26, { type: "mail", subject: "[GitHub] New sign-in to your account" }],
    ],
  },
  {
    source: "feishu", platform: "feishu", kind: "group", title: "严肃 Agent 交流群", avatar: "",
    last_message_at: ago(60 * 30), last_preview: "有人试过把 MCP server 挂到家里的 Mac mini 上吗", last_sender: "Rui",
    unread: 9, mentions: 0, needs_attention: false, is_alert: false,
    msgs: [["Rui", false, "有人试过把 MCP server 挂到家里的 Mac mini 上吗", 60 * 30]],
  },
  {
    source: "wechat", platform: "wechat", kind: "p2p", title: "顺丰速运", avatar: "",
    last_message_at: ago(60 * 50), last_preview: "您的快件已由前台代收", last_sender: "顺丰速运",
    unread: 0, mentions: 0, needs_attention: false, is_alert: false,
    msgs: [["顺丰速运", false, "您的快件已由前台代收", 60 * 50, { type: "link" }]],
  },
];

const convs: Conversation[] = seeds.map((s, i) => {
  const { msgs: _m, ...rest } = s;
  void _m;
  return { ...rest, id: `${s.source}:c${i}`, can_send: s.platform === "feishu" || s.platform === "mail" };
});

const messages: Record<string, Message[]> = {};
seeds.forEach((s, i) => {
  const cid = convs[i].id;
  messages[cid] = s.msgs.map(([name, self, text, min, extra], j) => ({
    id: `${cid}:m${j}`, conversation_id: cid, sender_id: self ? "me" : name, sender_name: name,
    is_self: self, type: "text", text, subject: "", sent_at: ago(min), mentions_me: false, ...extra,
  }));
});

function match(c: Conversation, v: View): boolean {
  if (v === "alert") return c.is_alert;
  if (c.is_alert) return false;
  switch (v) {
    case "needs": return c.needs_attention;
    case "unread": return c.unread > 0;
    case "mention": return c.mentions > 0;
    case "p2p": return c.kind === "p2p";
    case "group": return c.kind === "group";
    default: return true;
  }
}

// ---------- agent orchestration mock ----------

const mockNodes: NodeStatus[] = [
  {
    name: "mac-mini", online: true, last_seen: ago(0.1),
    info: {
      name: "mac-mini",
      runtimes: [{ name: "claude-code", version: "2.1.328 (Claude Code)" }, { name: "codex", version: "codex-cli 0.170.0" }],
      workspaces: [{ name: "life-agent", path: "~/Project/life-agent", worktree: true }, { name: "skill-hub", path: "~/project/skill-hub", worktree: true }],
    },
  },
  {
    name: "work-laptop", online: true, last_seen: ago(0.3),
    info: {
      name: "work-laptop",
      runtimes: [{ name: "claude-code", version: "2.1.328 (Claude Code)" }, { name: "codex", version: "", error: "codex 不可用：未安装" }],
      workspaces: [{ name: "tos-increment", path: "~/Project/tos-increment", worktree: true }],
    },
  },
  { name: "qq-vm", online: false, last_seen: ago(180) },
];

const mockAgents: AgentPreset[] = [
  { name: "life-agent-dev", title: "life-agent 开发", node: "mac-mini", workspace: "life-agent", runtime: "claude-code", available: true,
    instructions: "保持实现精简，完成后跑 make test。" },
  { name: "tos-dev", title: "TOS 开发", node: "work-laptop", workspace: "tos-increment", runtime: "claude-code", available: false,
    reason: "节点 work-laptop 不在线" },
];

const tasksM: Task[] = [
  {
    id: "t261119-a1b2c3", title: "排查计量异动告警 · 火山TOS-计量…", agent: "tos-dev", instruction: "帮我排查这个告警，给出原因和处理建议",
    status: "waiting_approval", node: "work-laptop", workspace: "tos-increment", runtime: "claude-code", model: "",
    conversation_id: "feishu:c0", session_id: "fdaa0743-e65d-42bb-a042-bb90640d4070",
    workdir: "/Users/me/life-agent/data/node/worktrees/tos-increment/t261119-a1b2c3", result: "", error: "", cost_usd: 0.42,
    created_at: ago(9), updated_at: ago(0.5), pending_approvals: 1,
  },
  {
    id: "t261119-d4e5f6", title: "补计量方案的风险项 · 存储产品周会", instruction: "按群里的需求补一下计量方案的风险项，并起草回复",
    status: "idle", node: "mac-mini", workspace: "life-agent", runtime: "claude-code", model: "",
    conversation_id: "feishu:c0", session_id: "2b1c…", workdir: "/Users/me/life-agent/data/node/worktrees/life-agent/t261119-d4e5f6",
    result: "已在 docs/billing-risk.md 补充 4 条风险项（数据延迟、重复计费、跨区复制计量、回滚口径），分支 life-agent/t261119-d4e5f6。\n\n```reply\n风险项已补到文档第 3 节：数据延迟、重复计费、跨区复制计量、回滚口径四类，各自给了缓解方案。周四评审前我再过一遍 👍\n```",
    error: "", cost_usd: 1.37, created_at: ago(60), updated_at: ago(22), pending_approvals: 0,
  },
  {
    id: "t261118-9f8e7d", title: "升级 skill-hub 的 eat 技能评测集", instruction: "给 eat 技能补 5 条评测用例",
    status: "failed", node: "mac-mini", workspace: "skill-hub", runtime: "codex", model: "",
    conversation_id: "", session_id: "01a1f375-99ff", workdir: "/Users/me/life-agent/data/node/worktrees/skill-hub/t261118-9f8e7d",
    result: "", error: "codex 退出：exit status 1：sandbox denied network access", cost_usd: 0, created_at: ago(60 * 20), updated_at: ago(60 * 19), pending_approvals: 0,
  },
];

const evs: Record<string, TaskEvent[]> = {};
const pushEv = (id: string, type: TaskEventType, payload: TaskEventPayload, min: number) => {
  const list = (evs[id] ??= []);
  list.push({ seq: list.length + 1, type, payload, at: ago(min) });
};
{
  const a = "t261119-a1b2c3";
  pushEv(a, "user", { text: "帮我排查这个告警，给出原因和处理建议" }, 9);
  pushEv(a, "state", { text: "running", input: tasksM[0].workdir }, 9);
  pushEv(a, "session", { session_id: tasksM[0].session_id }, 9);
  pushEv(a, "text", { text: "先看一下告警卡片里提到的计量任务和最近的发布记录。" }, 8.5);
  pushEv(a, "tool", { tool: "Grep", input: "metering_delay" }, 8.4);
  pushEv(a, "tool_result", { text: "internal/metering/collector.go:212: if delay > threshold {\ninternal/metering/collector.go:240: metrics.MeteringDelay.Observe(delay)" }, 8.3);
  pushEv(a, "tool", { tool: "Read", input: "internal/metering/collector.go" }, 8);
  pushEv(a, "thinking", { text: "延迟可能来自 Kafka 消费积压，需要查一下 consumer lag。" }, 7);
  pushEv(a, "tool", { tool: "Bash", input: "kubectl -n tos get pods -l app=metering-collector" }, 1);
  pushEv(a, "approval", { id: "ap-1", task_id: a, tool: "Bash", input: "kubectl -n tos get pods -l app=metering-collector", reason: "执行命令", expires_at: new Date(now + 24 * 60_000).toISOString() }, 1);

  const b = "t261119-d4e5f6";
  pushEv(b, "user", { text: tasksM[1].instruction }, 60);
  pushEv(b, "state", { text: "running", input: tasksM[1].workdir }, 60);
  pushEv(b, "text", { text: "我先读一下现有的计量方案文档。" }, 59);
  pushEv(b, "tool", { tool: "Read", input: "docs/billing.md" }, 58);
  pushEv(b, "tool", { tool: "Edit", input: "docs/billing-risk.md" }, 40);
  pushEv(b, "tool", { tool: "Bash", input: "git commit -am \"docs: billing risks\"" }, 30);
  pushEv(b, "approval", { id: "ap-0", task_id: b, tool: "Bash", input: "git commit -am \"docs: billing risks\"", reason: "执行命令", expires_at: ago(5) }, 30);
  pushEv(b, "approval_decided", { id: "ap-0", status: "allowed", by: "me@github" }, 29);
  pushEv(b, "result", { text: tasksM[1].result, cost_usd: 1.37 }, 22);
  pushEv(b, "state", { text: "idle" }, 22);

  const c = "t261118-9f8e7d";
  pushEv(c, "user", { text: tasksM[2].instruction }, 60 * 20);
  pushEv(c, "state", { text: "running", input: tasksM[2].workdir }, 60 * 20);
  pushEv(c, "log", { text: "`[features].codex_hooks` is deprecated." }, 60 * 20);
  pushEv(c, "tool", { tool: "shell", input: "curl -sL https://example.com/evals.json" }, 60 * 19.5);
  pushEv(c, "tool_result", { text: "exit 1\ncurl: (6) Could not resolve host", is_error: true }, 60 * 19.5);
  pushEv(c, "error", { text: "sandbox denied network access" }, 60 * 19);
  pushEv(c, "state", { text: "failed", input: tasksM[2].error }, 60 * 19);
}

const approvalsM: Approval[] = [
  { id: "ap-1", task_id: "t261119-a1b2c3", task_title: tasksM[0].title, tool: "Bash", input: "kubectl -n tos get pods -l app=metering-collector", reason: "执行命令", status: "pending", decided_by: "", created_at: ago(1), expires_at: new Date(now + 24 * 60_000).toISOString() },
  { id: "ap-0", task_id: "t261119-d4e5f6", task_title: tasksM[1].title, tool: "Bash", input: "git commit -am \"docs: billing risks\"", reason: "执行命令", status: "allowed", decided_by: "me@github", created_at: ago(30), expires_at: ago(5) },
];

const liveApprovals = () => approvalsM.filter((a) => a.status === "pending" && new Date(a.expires_at).getTime() > Date.now());
const refreshPending = () => tasksM.forEach((t) => { t.pending_approvals = liveApprovals().filter((a) => a.task_id === t.id).length; });

export const mock = {
  sources: (): Source[] => mockSources.map((s) => (s.state === "ok" ? { ...s, last_sync_at: new Date(Date.now() - Math.random() * 12000).toISOString() } : s)),
  counts: (): Counts => {
    const views: View[] = ["needs", "all", "unread", "mention", "p2p", "group", "alert"];
    const c = Object.fromEntries(views.map((v) => [v, convs.filter((c) => match(c, v)).length])) as Counts;
    c.tasks_active = tasksM.filter((t) => t.status !== "done" && t.status !== "cancelled").length;
    c.approvals = liveApprovals().length;
    return c;
  },
  conversations: (view: View, source?: string): Conversation[] =>
    convs.filter((c) => match(c, view) && (!source || c.source === source))
      .sort((a, b) => b.last_message_at.localeCompare(a.last_message_at)),
  messages: (id: string): Message[] => messages[id] ?? [],
  read: (id: string) => { const c = convs.find((x) => x.id === id); if (c) { c.unread = 0; c.mentions = 0; } },
  done: (id: string) => { const c = convs.find((x) => x.id === id); if (c) c.needs_attention = false; },
  send: (id: string, text: string) => {
    const c = convs.find((x) => x.id === id);
    if (!c) return;
    const m: Message = { id: `${id}:m${Date.now()}`, conversation_id: id, sender_id: "me", sender_name: "我", is_self: true, type: "text", text, subject: "", sent_at: new Date().toISOString(), mentions_me: false };
    (messages[id] ??= []).push(m);
    c.last_message_at = m.sent_at; c.last_preview = text; c.last_sender = "我"; c.needs_attention = false;
  },
  search: (q: string): SearchHit[] => {
    const out: SearchHit[] = [];
    for (const c of convs) for (const m of messages[c.id] ?? []) {
      if (m.text.includes(q) || m.subject.includes(q) || c.title.includes(q)) out.push({ ...m, conversation_title: c.title, platform: c.platform });
    }
    return out.slice(0, 50);
  },

  nodes: (): NodeStatus[] => mockNodes,
  agents: (): AgentPreset[] => mockAgents,
  tasks: (status: string): Task[] => {
    refreshPending();
    return tasksM.filter((t) => {
      if (status === "active") return t.status !== "done" && t.status !== "cancelled";
      if (!status) return true;
      return t.status === status;
    }).sort((a, b) => b.updated_at.localeCompare(a.updated_at));
  },
  task: (id: string): Task => {
    refreshPending();
    const t = tasksM.find((x) => x.id === id);
    if (!t) throw new Error("not found");
    return t;
  },
  taskEvents: (id: string, after: number): TaskEvent[] => (evs[id] ?? []).filter((e) => e.seq > after),
  createTask: (b: CreateTaskReq): Task => {
    const id = `t261119-${Math.random().toString(16).slice(2, 8)}`;
    const p = b.agent ? mockAgents.find((a) => a.name === b.agent) : undefined;
    const t: Task = {
      id, title: b.title || b.instruction.slice(0, 28), agent: b.agent ?? "", instruction: b.instruction, status: "running",
      node: p?.node ?? b.node ?? "", workspace: p?.workspace ?? b.workspace ?? "",
      runtime: p?.runtime ?? b.runtime ?? "claude-code", model: b.model ?? "", conversation_id: b.conversation_id ?? "", session_id: "", workdir: `/tmp/worktrees/${p?.workspace ?? b.workspace}/${id}`,
      result: "", error: "", cost_usd: 0, created_at: new Date().toISOString(), updated_at: new Date().toISOString(), pending_approvals: 0,
    };
    tasksM.unshift(t);
    pushEv(id, "user", { text: b.instruction }, 0);
    pushEv(id, "state", { text: "running", input: t.workdir }, 0);
    pushEv(id, "text", { text: "（示例数据）收到，开始处理…" }, 0);
    return t;
  },
  taskInput: (id: string, text: string) => {
    const t = tasksM.find((x) => x.id === id);
    if (!t) return;
    pushEv(id, "user", { text }, 0);
    pushEv(id, "state", { text: "running", input: t.workdir }, 0);
    t.status = "running"; t.updated_at = new Date().toISOString();
  },
  taskCancel: (id: string) => {
    const t = tasksM.find((x) => x.id === id);
    if (!t) return;
    pushEv(id, "state", { text: "cancelled" }, 0);
    t.status = "cancelled"; t.updated_at = new Date().toISOString();
  },
  taskClose: (id: string) => { const t = tasksM.find((x) => x.id === id); if (t) t.status = "done"; },
  approvals: (pendingOnly: boolean, taskId?: string): Approval[] =>
    (pendingOnly ? liveApprovals() : approvalsM).filter((a) => !taskId || a.task_id === taskId),
  decide: (id: string, allow: boolean): { approval: Approval; conflict?: "already" | "expired" } => {
    const a = approvalsM.find((x) => x.id === id);
    if (!a) throw new Error("not found");
    if (a.status !== "pending") return { approval: a, conflict: "already" };
    a.status = allow ? "allowed" : "denied"; a.decided_by = "local";
    pushEv(a.task_id, "approval_decided", { id, status: a.status, by: "local" }, 0);
    const t = tasksM.find((x) => x.id === a.task_id);
    if (t && t.status === "waiting_approval") t.status = "running";
    refreshPending();
    return { approval: a };
  },
};
