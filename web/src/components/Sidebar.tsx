import {
  AppWindow, Bell, BookOpen, ChevronDown, ChevronRight, CircleGauge, Footprints, HeartPulse, House,
  Inbox, Keyboard, Landmark, ListTodo, MessageSquareText, Network, Radar, Receipt,
  Repeat, Search, Settings2, Sun, Video, Wallet,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

export type NavKey = string;

interface Item { key: NavKey; label: string; icon: LucideIcon; badge?: number; tone?: string }
interface Group { title: string; items: Item[]; collapsed?: boolean }

export function Sidebar({ active, onNavigate, onSearch, messageBadge }: {
  active: NavKey;
  onNavigate: (k: NavKey) => void;
  onSearch: () => void;
  messageBadge: number;
}) {
  const top: Item[] = [
    { key: "today", label: "今天", icon: Sun, tone: "#C98A1B" },
    { key: "inbox", label: "收件箱", icon: Inbox, tone: "#B5652F" },
  ];
  const groups: Group[] = [
    { title: "收藏", items: [
      { key: "messages", label: "消息", icon: MessageSquareText, badge: messageBadge, tone: "#B5652F" },
      { key: "todo", label: "待办", icon: ListTodo, tone: "#3E6FB0" },
      { key: "journal", label: "日记", icon: BookOpen, tone: "#B04A6E" },
    ] },
    { title: "工作台", items: [], collapsed: true },
    { title: "生活", items: [
      { key: "footprints", label: "足迹", icon: Footprints, tone: "#9C5B3B" },
      { key: "health", label: "健康", icon: HeartPulse, tone: "#C0466A" },
    ] },
    { title: "资产", items: [
      { key: "subscriptions", label: "订阅", icon: Repeat, tone: "#3C7A55" },
      { key: "reimburse", label: "报销", icon: Receipt, tone: "#3C7A55" },
      { key: "bills", label: "账单", icon: Wallet, tone: "#3C7A55" },
      { key: "finance", label: "财务", icon: Landmark, tone: "#3C7A55" },
    ] },
    { title: "Homelab", items: [
      { key: "home", label: "智能家居", icon: House, tone: "#9C5B3B" },
      { key: "camera", label: "摄像头", icon: Video, tone: "#9C5B3B" },
      { key: "overview", label: "概览", icon: CircleGauge, tone: "#3E6FB0" },
      { key: "apps", label: "应用", icon: AppWindow, tone: "#3E6FB0" },
      { key: "network", label: "路由 · 代理", icon: Network, tone: "#3E6FB0" },
      { key: "tailscale", label: "Tailscale", icon: Radar, tone: "#3C7A55" },
      { key: "events", label: "事件 · 告警", icon: Bell, tone: "#C0392B" },
    ] },
  ];

  const row = (it: Item) => {
    const Icon = it.icon;
    return (
      <button key={it.key} className={`nav-row${active === it.key ? " is-active" : ""}`} onClick={() => onNavigate(it.key)}>
        <span className="nav-icon" style={{ color: it.tone }}><Icon size={16} strokeWidth={1.8} /></span>
        <span className="nav-label">{it.label}</span>
        {it.badge ? <span className="nav-badge">{it.badge > 99 ? "99+" : it.badge}</span> : null}
      </button>
    );
  };

  return (
    <aside className="sidebar">
      <button className="workspace">
        <span className="workspace-logo">L</span>
        <span className="workspace-name">Life Agent</span>
        <ChevronDown size={14} className="muted" />
      </button>

      <nav className="nav-scroll">
        <button className="nav-row" onClick={onSearch}>
          <span className="nav-icon"><Search size={16} strokeWidth={1.8} /></span>
          <span className="nav-label">搜索</span>
          <kbd className="nav-kbd">⌘K</kbd>
        </button>
        {top.map(row)}

        {groups.map((g) => (
          <div key={g.title} className="nav-group">
            <div className="nav-group-title">
              <span>{g.title}</span>
              {g.collapsed ? <ChevronRight size={14} /> : null}
            </div>
            {g.items.map(row)}
          </div>
        ))}
      </nav>

      <div className="nav-footer">
        {row({ key: "settings", label: "设置", icon: Settings2 })}
        {row({ key: "shortcuts", label: "快捷键", icon: Keyboard })}
      </div>
    </aside>
  );
}
