import type { Conversation, Counts, Message, SearchHit, Source, View } from "./types";

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

export const mock = {
  sources: (): Source[] => mockSources.map((s) => (s.state === "ok" ? { ...s, last_sync_at: new Date(Date.now() - Math.random() * 12000).toISOString() } : s)),
  counts: (): Counts => {
    const views: View[] = ["needs", "all", "unread", "mention", "p2p", "group", "alert"];
    return Object.fromEntries(views.map((v) => [v, convs.filter((c) => match(c, v)).length])) as Counts;
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
};
