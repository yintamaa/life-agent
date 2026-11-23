import { Construction, Search } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { api, subscribe } from "./api";
import { HandoffModal } from "./components/HandoffModal";
import { MessagesPage } from "./components/MessagesPage";
import { SearchModal } from "./components/SearchModal";
import { Sidebar } from "./components/Sidebar";
import { TasksPage } from "./components/TasksPage";
import type { Conversation, Counts, TaskFilter } from "./types";

const LABELS: Record<string, string> = {
  today: "今天", inbox: "收件箱", messages: "消息", todo: "待办", journal: "日记", footprints: "足迹", health: "健康",
  subscriptions: "订阅", reimburse: "报销", bills: "账单", finance: "财务", home: "智能家居", camera: "摄像头",
  overview: "概览", apps: "应用", network: "路由 · 代理", tailscale: "Tailscale", events: "事件 · 告警",
  settings: "设置", shortcuts: "快捷键",
};

/** "#/todo?f=approvals&t=<id>" → { route: "todo", params } */
const parseHash = () => {
  const raw = location.hash.replace(/^#\/?/, "");
  const [path, query = ""] = raw.split("?");
  return { route: path || "messages", params: new URLSearchParams(query), raw };
};
const FILTERS: TaskFilter[] = ["active", "approvals", "done", "all"];

export default function App() {
  const [hash, setHash] = useState(parseHash);
  const route = hash.route;
  const [searchOpen, setSearchOpen] = useState(false);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [jumpTo, setJumpTo] = useState<{ id: string; nonce: number }>();
  // conv set = "交给 Agent" from a conversation; conv undefined = standalone 新建任务
  const [handoff, setHandoff] = useState<{ conv?: Conversation } | null>(null);
  const [agentReady, setAgentReady] = useState(false);

  // Keep sidebar badges fresh on every page (one shared SSE connection).
  useEffect(() => {
    const load = () => api.counts().then(setCounts).catch(() => {});
    const loadNodes = () => api.nodes()
      .then((ns) => setAgentReady(ns.some((n) => n.online && (n.info?.workspaces.length ?? 0) > 0)))
      .catch(() => setAgentReady(false));
    load(); loadNodes();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsub = subscribe(() => { clearTimeout(timer); timer = setTimeout(load, 600); });
    const t = setInterval(loadNodes, 30_000);
    return () => { clearTimeout(timer); unsub(); clearInterval(t); };
  }, []);

  useEffect(() => {
    const onHash = () => setHash(parseHash());
    window.addEventListener("hashchange", onHash);
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setSearchOpen((o) => !o); }
    };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("hashchange", onHash); window.removeEventListener("keydown", onKey); };
  }, []);

  const navigate = (k: string) => { location.hash = `/${k}`; };
  const onCounts = useCallback((c: Counts) => setCounts(c), []);
  const todoBadge = (counts?.approvals ?? 0) || (counts?.tasks_active ?? 0);
  const f = hash.params.get("f");

  return (
    <div className="app">
      <Sidebar active={route} onNavigate={navigate} onSearch={() => setSearchOpen(true)}
        messageBadge={counts?.needs ?? 0} todoBadge={todoBadge} todoAlert={(counts?.approvals ?? 0) > 0} />
      <main className="main">
        <div className="topbar">
          <div className="crumbs">
            <span className="muted">工作台</span>
            <span className="crumb-sep">/</span>
            <span>{LABELS[route] ?? route}</span>
          </div>
          <button className="topbar-search" onClick={() => setSearchOpen(true)}>
            <Search size={14} /> <span>搜索</span> <kbd className="nav-kbd">⌘ K</kbd>
          </button>
        </div>
        {route === "messages" ? (
          <MessagesPage onCounts={onCounts} jumpTo={jumpTo} agentReady={agentReady} onHandoff={(conv) => setHandoff({ conv })}
            onOpenApprovals={() => navigate("todo?f=approvals")} />
        ) : route === "todo" ? (
          <TasksPage onCounts={onCounts} nav={hash.raw} onNewTask={() => setHandoff({})}
            initialFilter={f && (FILTERS as string[]).includes(f) ? (f as TaskFilter) : undefined}
            initialTask={hash.params.get("t") ?? undefined} />
        ) : (
          <div className="placeholder">
            <Construction size={28} strokeWidth={1.4} />
            <h2>{LABELS[route] ?? route}</h2>
            <p>即将推出 · 先把 IM 打通</p>
          </div>
        )}
      </main>
      <SearchModal
        open={searchOpen}
        onClose={() => setSearchOpen(false)}
        onPick={(h) => { setSearchOpen(false); navigate("messages"); setJumpTo({ id: h.conversation_id, nonce: Date.now() }); }}
      />
      {handoff && (
        <HandoffModal conv={handoff.conv} onClose={() => setHandoff(null)}
          onCreated={(t) => { setHandoff(null); navigate(`todo?t=${encodeURIComponent(t.id)}`); }} />
      )}
    </div>
  );
}
