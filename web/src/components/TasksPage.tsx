import { Bot, CheckCheck, ListTodo, Loader, Plus, ShieldAlert, Inbox as InboxIcon, Server } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { LucideIcon } from "lucide-react";
import { api, ApiError, isMock, subscribe } from "../api";
import { relTime } from "../lib/time";
import { RUNTIME_LABEL, STATUS } from "../lib/task";
import { ApprovalBanner } from "./ApprovalBanner";
import { PushControl } from "./PushControl";
import { TaskDetail } from "./TaskDetail";
import type { Counts, NodeStatus, Task, TaskFilter } from "../types";

const FILTERS: Array<{ f: TaskFilter; label: string; icon: LucideIcon }> = [
  { f: "active", label: "进行中", icon: Loader },
  { f: "approvals", label: "待审批", icon: ShieldAlert },
  { f: "done", label: "已完成", icon: CheckCheck },
  { f: "all", label: "全部", icon: InboxIcon },
];

const EMPTY: Record<TaskFilter, string> = {
  active: "没有进行中的任务。点「新建任务」，或在任意会话里点「交给 Agent」",
  approvals: "没有等待审批的操作 ✓",
  done: "还没有已关闭的任务",
  all: "还没有任务",
};

function TaskRow({ t, selected, onSelect, agentTitle }: { t: Task; selected: boolean; onSelect: () => void; agentTitle?: string }) {
  const st = STATUS[t.status];
  return (
    <li>
      <button className={`task-row${selected ? " is-selected" : ""}`} onClick={onSelect}>
        <span className={`task-dot tone-${st.tone}`} aria-hidden />
        <span className="conv-main">
          <span className="conv-line1">
            <span className="conv-title">{t.title}</span>
            <span className="conv-time">{relTime(t.updated_at)}</span>
          </span>
          <span className="conv-line2">
            <span className={`task-pill tone-${st.tone}`}>{st.label}</span>
            {agentTitle && <span className="task-agent">{agentTitle}</span>}
            <span className="task-chips">{t.node} · {t.workspace} · {RUNTIME_LABEL[t.runtime] ?? t.runtime}</span>
            {t.pending_approvals > 0 && <span className="task-approvals" title="等待审批"><ShieldAlert size={12} /> {t.pending_approvals}</span>}
          </span>
        </span>
      </button>
    </li>
  );
}

export function TasksPage({ onCounts, initialFilter, initialTask, nav, onNewTask }: {
  onCounts: (c: Counts) => void;
  onNewTask: () => void;
  initialFilter?: TaskFilter;
  initialTask?: string;
  nav: string; // changes whenever the hash changes, to re-apply deep links
}) {
  const [filter, setFilter] = useState<TaskFilter>(initialFilter ?? "active");
  const [tasks, setTasks] = useState<Task[]>([]);
  const [selected, setSelected] = useState<string | null>(initialTask ?? null);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [nodes, setNodes] = useState<NodeStatus[]>([]);
  const [disabled, setDisabled] = useState("");
  const [loading, setLoading] = useState(true);
  const [version, setVersion] = useState(0);
  const [agentTitles, setAgentTitles] = useState<Record<string, string>>({});
  useEffect(() => {
    api.agents().then((as) => setAgentTitles(Object.fromEntries(as.map((a) => [a.name, a.title || a.name])))).catch(() => {});
  }, []);
  const titleOf = (t: Task) => (t.agent ? agentTitles[t.agent] ?? t.agent : undefined);

  // deep links: #/todo?f=approvals&t=<task id>
  useEffect(() => {
    if (initialFilter) setFilter(initialFilter);
    if (initialTask) setSelected(initialTask);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nav]);

  const loadCounts = useCallback(() => api.counts().then((c) => { setCounts(c); onCounts(c); }).catch(() => {}), [onCounts]);
  const loadNodes = useCallback(() => api.nodes().then((n) => { setNodes(n); setDisabled(""); }).catch((e) => {
    if (e instanceof ApiError && e.status === 503) setDisabled(e.message);
  }), []);
  const loadList = useCallback(() => {
    const status = filter === "approvals" || filter === "all" ? (filter === "all" ? "" : "active") : filter;
    return api.tasks(status)
      .then((list) => setTasks(filter === "approvals" ? list.filter((t) => t.pending_approvals > 0) : list))
      .catch((e) => { if (e instanceof ApiError && e.status === 503) setDisabled(e.message); })
      .finally(() => setLoading(false));
  }, [filter]);

  useEffect(() => { setLoading(true); loadList(); }, [loadList]);
  useEffect(() => { loadCounts(); loadNodes(); const t = setInterval(loadNodes, 30_000); return () => clearInterval(t); }, [loadCounts, loadNodes]);

  const refresh = useRef<() => void>(() => {});
  refresh.current = () => { loadList(); loadCounts(); setVersion((v) => v + 1); };
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsub = subscribe(() => { clearTimeout(timer); timer = setTimeout(() => refresh.current(), 400); });
    // Mock mode has no SSE: poll so the timeline still feels alive.
    const poll = isMock() ? setInterval(() => refresh.current(), 5000) : undefined;
    return () => { clearTimeout(timer); unsub(); if (poll) clearInterval(poll); };
  }, []);

  const pick = (id: string | null) => {
    setSelected(id);
    history.replaceState(null, "", `#/todo${id ? `?t=${encodeURIComponent(id)}` : filter !== "active" ? `?f=${filter}` : ""}`);
  };

  const online = nodes.filter((n) => n.online);
  const approvals = counts?.approvals ?? 0;

  return (
    <div className={`page${selected ? " has-detail" : ""}`}>
      <header className="page-head">
        <div className="page-title-row">
          <span className="page-icon page-icon-todo"><ListTodo size={20} strokeWidth={1.8} /></span>
          <h1>待办</h1>
          {isMock() && <span className="mock-tag" title="后端未连接，正在展示示例数据">示例数据</span>}
          <span className="page-title-actions">
            <button className="btn btn-primary" onClick={onNewTask} disabled={!!disabled}><Plus size={14} /> 新建任务</button>
          </span>
        </div>
        <PushControl />
        <p className="page-sub">交给 Agent 的任务；需要你批准的操作会推到这里，任何设备上先点的那一下生效</p>
        {filter !== "approvals" && <ApprovalBanner count={approvals} onOpen={() => { setFilter("approvals"); pick(null); }} />}
        <div className="tabs" role="tablist">
          {FILTERS.map(({ f, label, icon: Icon }) => {
            const n = f === "approvals" ? approvals : f === "active" ? counts?.tasks_active ?? 0 : 0;
            return (
              <button key={f} role="tab" aria-selected={filter === f}
                className={`tab${filter === f ? " is-active" : ""}${f === "approvals" && n > 0 ? " tone-warn" : ""}`}
                onClick={() => setFilter(f)}>
                <Icon size={15} strokeWidth={1.9} />
                <span>{label}</span>
                {n > 0 ? <span className="tab-count">{n}</span> : null}
              </button>
            );
          })}
        </div>
      </header>

      <div className="node-strip">
        {disabled ? (
          <span className="node-off">未启用 Agent 协同</span>
        ) : nodes.length === 0 ? (
          <span className="muted">没有节点在线</span>
        ) : nodes.map((n) => (
          <span key={n.name} className={`node-pill${n.online ? " is-online" : ""}`}
            title={n.online ? n.info?.runtimes.map((r) => `${RUNTIME_LABEL[r.name] ?? r.name}${r.error ? `（${r.error}）` : ` ${r.version}`}`).join("\n") : `离线 · 最后在线 ${relTime(n.last_seen)}`}>
            <Server size={12} /> <span className="source-name">{n.name}</span>
            <span className="source-dot" />
            <span className="source-sync">{n.online ? `${n.info?.workspaces.length ?? 0} 个工作区` : `离线 · ${relTime(n.last_seen)}`}</span>
          </span>
        ))}
      </div>

      {disabled ? (
        <div className="placeholder">
          <Bot size={28} strokeWidth={1.4} />
          <h2>还没有启用 Agent 协同</h2>
          <p>在 config.yaml 里打开 <code>node.enabled</code> 并配置工作区，或让其它机器用 <code>lifeagent node</code> 接入</p>
          <p className="muted">{disabled}</p>
        </div>
      ) : (
        <div className={`panes${selected ? " has-selection" : ""}`}>
          <div className="pane-list">
            {loading && tasks.length === 0 ? <div className="list-empty">加载中…</div>
              : tasks.length === 0 ? <div className="list-empty">{EMPTY[filter]}{filter === "active" && online.length === 0 ? <><br />（当前没有在线节点）</> : null}</div>
              : (
                <ul className="conv-list">
                  {tasks.map((t) => <TaskRow key={t.id} t={t} selected={t.id === selected} onSelect={() => pick(t.id)} agentTitle={titleOf(t)} />)}
                </ul>
              )}
          </div>
          <div className="pane-thread">
            {selected ? (
              <>
                <button className="back-mobile" onClick={() => pick(null)}>← 返回</button>
                <TaskDetail taskId={selected} version={version} onChanged={() => refresh.current()} agentTitles={agentTitles} />
              </>
            ) : (
              <div className="thread-placeholder">
                <Bot size={28} strokeWidth={1.4} />
                <span>选择一个任务</span>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
