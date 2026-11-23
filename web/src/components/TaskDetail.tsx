import {
  Ban, Brain, Check, ChevronRight, CircleAlert, Copy, CornerDownLeft, FileText, Reply, SendHorizontal, ShieldAlert,
  ShieldCheck, ShieldX, Square, Terminal, Wrench, X,
} from "lucide-react";
import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError, newOpKey } from "../api";
import { clockTime } from "../lib/time";
import { canContinue, countdown, extractReply, isLive, RUNTIME_LABEL, splitFences, STATUS } from "../lib/task";
import type { Approval, Task, TaskEvent } from "../types";

/** Renders text with ``` fences as <pre>, everything else pre-wrapped. */
export function RichText({ text }: { text: string }) {
  return (
    <>
      {splitFences(text).map((s, i) =>
        s.kind === "code" ? (
          <pre key={i} className="tl-pre" data-lang={s.lang || undefined}><code>{s.text}</code></pre>
        ) : (
          <span key={i} className="tl-prose">{s.text}</span>
        ),
      )}
    </>
  );
}

function useNow(active: boolean, ms = 1000) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [active, ms]);
  return now;
}

const TOOL_ICON: Record<string, typeof Terminal> = { Bash: Terminal, shell: Terminal, Read: FileText, Edit: FileText, Write: FileText };

type Item =
  | { kind: "event"; ev: TaskEvent }
  | { kind: "tool"; ev: TaskEvent; result?: TaskEvent };

/** Pair each tool call with the tool_result that follows it. */
function toItems(events: TaskEvent[]): Item[] {
  const items: Item[] = [];
  for (const ev of events) {
    if (ev.type === "session") continue;
    if (ev.type === "tool") { items.push({ kind: "tool", ev }); continue; }
    if (ev.type === "tool_result") {
      for (let i = items.length - 1; i >= 0; i--) {
        const it = items[i];
        if (it.kind === "tool" && !it.result) { it.result = ev; break; }
        if (it.kind === "tool") break;
      }
      continue;
    }
    if (ev.type === "approval_decided") continue; // shown on the approval card
    items.push({ kind: "event", ev });
  }
  return items;
}

function ToolRow({ ev, result }: { ev: TaskEvent; result?: TaskEvent }) {
  const [open, setOpen] = useState(false);
  const tool = ev.payload.tool ?? "tool";
  const Icon = TOOL_ICON[tool] ?? Wrench;
  const failed = result?.payload.is_error;
  return (
    <div className={`tl-tool${failed ? " is-error" : ""}`}>
      <button className="tl-tool-head" onClick={() => result?.payload.text && setOpen((o) => !o)} aria-expanded={open}>
        <Icon size={13} className="tl-tool-icon" />
        <span className="tl-tool-name">{tool}</span>
        <span className="tl-tool-input">{ev.payload.input}</span>
        {result?.payload.text ? <ChevronRight size={13} className={`tl-chev${open ? " is-open" : ""}`} /> : null}
      </button>
      {open && result?.payload.text && <pre className="tl-pre tl-tool-out">{result.payload.text}</pre>}
    </div>
  );
}

function ApprovalCard({ ev, approval, onDecided }: { ev: TaskEvent; approval?: Approval; onDecided: () => void }) {
  const p = ev.payload;
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [local, setLocal] = useState<Approval | null>(null);
  const a = local ?? approval;
  const status = a?.status ?? "pending";
  const expires = a?.expires_at ?? p.expires_at ?? "";
  const pending = status === "pending";
  const now = useNow(pending);
  const left = expires ? countdown(expires, now) : null;
  const expired = pending && expires !== "" && left === null;

  const decide = async (allow: boolean) => {
    if (!p.id || busy) return;
    setBusy(true); setNote("");
    try {
      const r = await api.decide(p.id, allow);
      setLocal(r.approval);
      if (r.conflict === "already") setNote(`已在其他设备处理：${r.approval.status === "allowed" ? "允许" : r.approval.status === "denied" ? "拒绝" : "已过期"}`);
      if (r.conflict === "expired") setNote("Agent 已不再等待（超时自动拒绝）");
      onDecided();
    } catch (e) {
      setNote(`操作失败：${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  const shown = expired ? "expired" : status;
  return (
    <div className={`tl-approval is-${shown}`}>
      <div className="tl-approval-head">
        {shown === "allowed" ? <ShieldCheck size={15} /> : shown === "denied" ? <ShieldX size={15} /> : <ShieldAlert size={15} />}
        <span className="tl-approval-title">
          {shown === "pending" ? "等待审批" : shown === "allowed" ? "已允许" : shown === "denied" ? "已拒绝" : "已过期（自动拒绝）"}
          <span className="tl-approval-tool"> · {p.tool}</span>
        </span>
        {shown === "pending" && left && <span className="tl-countdown" title="超时后自动拒绝">{left}</span>}
        {a?.decided_by && shown !== "pending" && <span className="tl-approval-by">{a.decided_by}</span>}
      </div>
      <pre className="tl-pre tl-approval-input">{p.input}</pre>
      {p.reason && <div className="tl-approval-reason">{p.reason}</div>}
      {shown === "pending" && (
        <div className="tl-approval-actions">
          <button className="btn btn-allow" disabled={busy} onClick={() => decide(true)}><Check size={14} /> 允许</button>
          <button className="btn btn-ghost" disabled={busy} onClick={() => decide(false)}><X size={14} /> 拒绝</button>
        </div>
      )}
      {note && <div className="tl-approval-note">{note}</div>}
    </div>
  );
}

function StateDivider({ ev }: { ev: TaskEvent }) {
  const s = ev.payload.text;
  const label = s === "running" ? "开始运行" : s === "idle" ? "本轮结束" : s === "failed" ? "运行失败" : s === "cancelled" ? "已取消" : s;
  const detail = s === "running" ? ev.payload.input : s === "failed" ? ev.payload.input : "";
  return (
    <div className={`tl-state is-${s}`}>
      <span className="tl-state-line" />
      <span className="tl-state-label">{label}{detail ? <span className="tl-state-detail"> · {detail}</span> : null} · {clockTime(ev.at)}</span>
      <span className="tl-state-line" />
    </div>
  );
}

function EventView({ ev }: { ev: TaskEvent }) {
  const p = ev.payload;
  switch (ev.type) {
    case "user":
      return <div className="tl-user"><div className="bubble"><RichText text={p.text ?? ""} /></div></div>;
    case "text":
      return <div className="tl-text"><RichText text={p.text ?? ""} /></div>;
    case "thinking":
      return (
        <details className="tl-thinking">
          <summary><Brain size={13} /> 思考</summary>
          <div className="tl-thinking-body">{p.text}</div>
        </details>
      );
    case "state":
      return <StateDivider ev={ev} />;
    case "log":
      return <div className="tl-log">{p.text}</div>;
    case "error":
      return <div className="tl-error"><CircleAlert size={13} /> {p.text}</div>;
    case "result":
      return (
        <div className={`tl-result${p.is_error ? " is-error" : ""}`}>
          <div className="tl-result-head">{p.is_error ? "失败" : "结果"}{p.cost_usd ? <span className="tl-cost">${p.cost_usd.toFixed(2)}</span> : null}</div>
          <RichText text={p.text ?? ""} />
        </div>
      );
  }
  return null;
}

function ReplyCard({ task, reply }: { task: Task; reply: string }) {
  const [text, setText] = useState(reply);
  const [state, setState] = useState<"idle" | "sending" | "sent" | "unsupported">("idle");
  const [err, setErr] = useState("");
  const key = useRef<{ key: string; text: string } | null>(null);
  useEffect(() => { setText(reply); setState("idle"); setErr(""); key.current = null; }, [task.id, reply]);

  const send = async () => {
    const t = text.trim();
    if (!t || state === "sending" || state === "sent") return;
    if (!key.current || key.current.text !== t) key.current = { key: newOpKey(), text: t };
    setState("sending"); setErr("");
    try {
      await api.send(task.conversation_id, t, key.current.key);
      setState("sent");
    } catch (e) {
      if (e instanceof ApiError && e.status === 501) { setState("unsupported"); return; }
      if (e instanceof ApiError && e.status !== 409) key.current = null; // final answer → new key on manual retry
      setErr(e instanceof ApiError && e.status === 409 ? "这条回复还在发送中" : `发送失败：${(e as Error).message}`);
      setState("idle");
    }
  };

  return (
    <div className="reply-card">
      <div className="reply-card-head"><Reply size={14} /> 发回原会话 <span className="muted">· Agent 起草，确认后发送</span></div>
      <textarea value={text} onChange={(e) => setText(e.target.value)} disabled={state === "sent"} rows={3} />
      <div className="reply-card-actions">
        {state === "sent" ? (
          <span className="reply-sent"><Check size={14} /> 已发送</span>
        ) : state === "unsupported" ? (
          <>
            <span className="muted">该渠道暂不支持发送，请复制后在客户端回复</span>
            <button className="btn btn-ghost" onClick={() => navigator.clipboard?.writeText(text)}><Copy size={13} /> 复制</button>
          </>
        ) : (
          <>
            {err && <span className="composer-err">{err}</span>}
            <button className="btn btn-primary" disabled={!text.trim() || state === "sending"} onClick={send}>
              <SendHorizontal size={14} /> {state === "sending" ? "发送中…" : "发送"}
            </button>
          </>
        )}
      </div>
    </div>
  );
}

export function TaskDetail({ taskId, version, onChanged, agentTitles = {} }: {
  taskId: string; version: number; onChanged: () => void; agentTitles?: Record<string, string>;
}) {
  const [task, setTask] = useState<Task | null>(null);
  const [events, setEvents] = useState<TaskEvent[]>([]);
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [err, setErr] = useState("");
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [actionErr, setActionErr] = useState("");
  const [copied, setCopied] = useState(false);
  const lastSeq = useRef(0);
  const current = useRef(taskId);
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  // Reset on task switch.
  useEffect(() => {
    current.current = taskId;
    lastSeq.current = 0;
    setTask(null); setEvents([]); setApprovals([]); setErr(""); setInput(""); setActionErr("");
    stick.current = true;
  }, [taskId]);

  // Load task + incremental events (seq > lastSeq) on open and on every SSE tick.
  useEffect(() => {
    let live = true;
    const id = taskId;
    api.task(id).then((t) => live && current.current === id && setTask(t)).catch((e) => live && setErr(String(e.message ?? e)));
    api.taskEvents(id, lastSeq.current).then((evs) => {
      if (!live || current.current !== id || evs.length === 0) return;
      const fresh = evs.filter((e) => e.seq > lastSeq.current);
      if (!fresh.length) return;
      lastSeq.current = fresh[fresh.length - 1].seq;
      setEvents((prev) => [...prev, ...fresh]);
    }).catch(() => {});
    api.approvals(false, id).then((a) => live && current.current === id && setApprovals(a)).catch(() => {});
    return () => { live = false; };
  }, [taskId, version]);

  useEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [events]);

  const items = useMemo(() => toItems(events), [events]);
  const byId = useMemo(() => Object.fromEntries(approvals.map((a) => [a.id, a])), [approvals]);
  const reply = task?.conversation_id ? extractReply(task.result) : null;

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true); setActionErr("");
    try { await fn(); onChanged(); } catch (e) { setActionErr((e as Error).message); } finally { setBusy(false); }
  };
  const submit = () => {
    const t = input.trim();
    if (!t || !task || !canContinue(task.status)) return;
    act(async () => { await api.taskInput(task.id, t); setInput(""); stick.current = true; });
  };

  if (err && !task) return <div className="thread-empty error">加载失败：{err}</div>;
  if (!task) return <div className="thread-empty">加载中…</div>;
  const st = STATUS[task.status];

  return (
    <section className="task-detail" aria-label={task.title}>
      <header className="thread-head task-head">
        <div className="thread-title">
          <span className="thread-name">{task.title}</span>
          <span className="thread-meta task-meta">
            <span className={`task-pill tone-${st.tone}`}>{st.label}</span>
            {task.agent && <span className="task-agent">{agentTitles[task.agent] ?? task.agent}</span>}
            <span className="task-chip">{task.node}</span>
            <span className="task-chip">{task.workspace}</span>
            <span className="task-chip">{RUNTIME_LABEL[task.runtime] ?? task.runtime}</span>
            {task.cost_usd > 0 && <span className="task-cost">${task.cost_usd.toFixed(2)}</span>}
          </span>
        </div>
        <div className="thread-actions">
          {isLive(task.status) && (
            <button className="btn btn-ghost" disabled={busy} onClick={() => act(() => api.taskCancel(task.id))}><Square size={13} /> 取消</button>
          )}
          {canContinue(task.status) && (
            <button className="btn btn-ghost" disabled={busy} onClick={() => act(() => api.taskClose(task.id))}><Ban size={13} /> 关闭任务</button>
          )}
        </div>
      </header>
      {task.workdir && (
        <button className="task-workdir" title="复制工作目录路径" onClick={() => {
          navigator.clipboard?.writeText(task.workdir).then(() => { setCopied(true); setTimeout(() => setCopied(false), 1200); }).catch(() => {});
        }}>
          <Copy size={12} /> <span>{task.workdir}</span> {copied && <span className="task-copied">已复制</span>}
        </button>
      )}

      <div className="thread-body task-body" ref={scroller}
        onScroll={(e) => { const el = e.currentTarget; stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80; }}>
        {items.length === 0 && <div className="thread-empty">等待 Agent 输出…</div>}
        {items.map((it) => (
          <Fragment key={it.ev.seq}>
            {it.kind === "tool" ? <ToolRow ev={it.ev} result={it.result} />
              : it.ev.type === "approval" ? <ApprovalCard ev={it.ev} approval={it.ev.payload.id ? byId[it.ev.payload.id] : undefined} onDecided={onChanged} />
              : <EventView ev={it.ev} />}
          </Fragment>
        ))}
        {task.error && task.status === "failed" && !events.some((e) => e.type === "state" && e.payload.text === "failed") && (
          <div className="tl-error"><CircleAlert size={13} /> {task.error}</div>
        )}
        {reply && <ReplyCard task={task} reply={reply} />}
      </div>

      <div className={`composer${canContinue(task.status) ? "" : " is-disabled"}`}
        title={canContinue(task.status) ? undefined : "Agent 正在运行，这一轮结束后可继续追问"}>
        <textarea rows={1} value={input} disabled={!canContinue(task.status) || busy}
          placeholder={canContinue(task.status) ? "继续追问或补充要求（Enter 发送，Shift+Enter 换行）" : task.status === "done" ? "任务已关闭" : "Agent 正在运行…"}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); } }} />
        {actionErr && <span className="composer-err">{actionErr}</span>}
        <button className="btn btn-primary" disabled={!input.trim() || !canContinue(task.status) || busy} onClick={submit} aria-label="发送">
          <CornerDownLeft size={14} />
        </button>
      </div>
    </section>
  );
}
