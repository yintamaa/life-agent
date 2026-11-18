import { Construction, Search } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { MessagesPage } from "./components/MessagesPage";
import { SearchModal } from "./components/SearchModal";
import { Sidebar } from "./components/Sidebar";
import type { Counts } from "./types";

const LABELS: Record<string, string> = {
  today: "今天", inbox: "收件箱", messages: "消息", todo: "待办", journal: "日记", footprints: "足迹", health: "健康",
  subscriptions: "订阅", reimburse: "报销", bills: "账单", finance: "财务", home: "智能家居", camera: "摄像头",
  overview: "概览", apps: "应用", network: "路由 · 代理", tailscale: "Tailscale", events: "事件 · 告警",
  settings: "设置", shortcuts: "快捷键",
};

const routeFromHash = () => (location.hash.replace(/^#\/?/, "") || "messages");

export default function App() {
  const [route, setRoute] = useState(routeFromHash);
  const [searchOpen, setSearchOpen] = useState(false);
  const [badge, setBadge] = useState(0);
  const [jumpTo, setJumpTo] = useState<{ id: string; nonce: number }>();

  useEffect(() => {
    const onHash = () => setRoute(routeFromHash());
    window.addEventListener("hashchange", onHash);
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); setSearchOpen((o) => !o); }
    };
    window.addEventListener("keydown", onKey);
    return () => { window.removeEventListener("hashchange", onHash); window.removeEventListener("keydown", onKey); };
  }, []);

  const navigate = (k: string) => { location.hash = `/${k}`; };
  const onCounts = useCallback((c: Counts) => setBadge(c.needs), []);

  return (
    <div className="app">
      <Sidebar active={route} onNavigate={navigate} onSearch={() => setSearchOpen(true)} messageBadge={badge} />
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
          <MessagesPage onCounts={onCounts} jumpTo={jumpTo} />
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
    </div>
  );
}
