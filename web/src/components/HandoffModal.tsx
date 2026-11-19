import { Bot, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError, newOpKey } from "../api";
import { RUNTIME_LABEL } from "../lib/task";
import type { Conversation, NodeStatus, RuntimeName, Task } from "../types";

/** "交给 Agent": create a task from a conversation, with its recent history as context. */
export function HandoffModal({ conv, onClose, onCreated }: {
  conv: Conversation;
  onClose: () => void;
  onCreated: (t: Task) => void;
}) {
  const [nodes, setNodes] = useState<NodeStatus[] | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [instruction, setInstruction] = useState("");
  const [target, setTarget] = useState(""); // "node\u0000workspace"
  const [runtime, setRuntime] = useState<RuntimeName>("claude-code");
  const [ctxN, setCtxN] = useState(30);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const opKey = useRef<string | null>(null);
  const area = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    setTimeout(() => area.current?.focus(), 0);
    api.nodes().then(setNodes).catch((e) => { setNodes([]); setLoadErr(e instanceof ApiError && e.status === 503 ? "未启用 Agent 协同（config.yaml 里配置 node）" : (e as Error).message); });
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const options = useMemo(() => (nodes ?? []).filter((n) => n.online && n.info).flatMap((n) =>
    n.info!.workspaces.map((w) => ({ key: `${n.name}\u0000${w.name}`, node: n, ws: w }))), [nodes]);
  useEffect(() => { if (!target && options.length) setTarget(options[0].key); }, [options, target]);
  const chosen = options.find((o) => o.key === target);
  const runtimes = (chosen?.node.info?.runtimes ?? []).filter((r) => !r.error);
  useEffect(() => {
    if (runtimes.length && !runtimes.some((r) => r.name === runtime)) setRuntime(runtimes[0].name);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, nodes]);

  // A changed form is a new intent → new idempotency key.
  useEffect(() => { opKey.current = null; }, [instruction, target, runtime, ctxN]);

  const submit = async () => {
    if (!chosen || !instruction.trim() || busy) return;
    opKey.current ??= newOpKey();
    setBusy(true); setErr("");
    try {
      const t = await api.createTask({
        instruction: instruction.trim(), conversation_id: conv.id, context_messages: ctxN,
        node: chosen.node.name, workspace: chosen.ws.name, runtime,
      }, opKey.current);
      onCreated(t);
    } catch (e) {
      // Server answered → that key is spent; network error → keep it for a safe retry.
      if (e instanceof ApiError && e.status !== 409) opKey.current = null;
      setErr(e instanceof ApiError && e.status === 409 ? "任务正在创建中，请稍候" : (e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const noNode = nodes !== null && options.length === 0;
  return (
    <div className="modal-scrim" onMouseDown={onClose}>
      <div className="modal handoff" role="dialog" aria-label="交给 Agent" onMouseDown={(e) => e.stopPropagation()}>
        <div className="handoff-head">
          <Bot size={16} /> <span>交给 Agent</span>
          <span className="muted handoff-src">· {conv.title}</span>
          <button className="handoff-x" onClick={onClose} aria-label="关闭"><X size={16} /></button>
        </div>
        <div className="handoff-body">
          <textarea ref={area} rows={4} value={instruction} onChange={(e) => setInstruction(e.target.value)}
            placeholder="帮我排查这个告警 / 按群里的需求改代码并起草回复"
            onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); submit(); } }} />
          <div className="handoff-grid">
            <label>
              <span>在哪干</span>
              <select value={target} onChange={(e) => setTarget(e.target.value)} disabled={noNode}>
                {noNode && <option>没有在线节点</option>}
                {options.map((o) => <option key={o.key} value={o.key}>{o.node.name} / {o.ws.name}</option>)}
              </select>
            </label>
            <label>
              <span>用谁</span>
              <select value={runtime} onChange={(e) => setRuntime(e.target.value as RuntimeName)} disabled={!runtimes.length}>
                {runtimes.map((r) => <option key={r.name} value={r.name}>{RUNTIME_LABEL[r.name] ?? r.name}</option>)}
              </select>
            </label>
            <label>
              <span>带上最近消息</span>
              <input type="number" min={0} max={200} value={ctxN} onChange={(e) => setCtxN(Math.max(0, Math.min(200, Number(e.target.value) || 0)))} />
            </label>
          </div>
          <p className="handoff-hint">
            {runtime === "codex"
              ? "Codex 在 workspace-write 沙箱里运行（只能写任务工作区、默认不联网），不逐步审批。"
              : "有副作用的操作会推送审批；只读工具、工作区内改文件、白名单命令自动放行。"}
            {" "}会话内容会作为上下文发给模型。
          </p>
          {(loadErr || err) && <div className="composer-err handoff-err">{loadErr || err}</div>}
        </div>
        <div className="handoff-foot">
          <button className="btn btn-ghost" onClick={onClose}>取消</button>
          <button className="btn btn-primary" disabled={!chosen || !instruction.trim() || busy} onClick={submit}
            title={noNode ? "没有在线节点" : "⌘ Enter"}>
            {busy ? "创建中…" : "交给 Agent"}
          </button>
        </div>
      </div>
    </div>
  );
}
