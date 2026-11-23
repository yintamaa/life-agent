import { Bot, Plus, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { api, ApiError, newOpKey } from "../api";
import { RUNTIME_LABEL } from "../lib/task";
import type { AgentPreset, Conversation, NodeStatus, RuntimeName, Task } from "../types";

const CUSTOM = "\u0000custom";

/**
 * Create a task. With `conv`: "交给 Agent" from a conversation (recent history
 * goes along as context). Without: a standalone "新建任务" from 待办.
 * Pick a named agent preset (one tap on a phone) or 自定义 node/workspace/runtime.
 */
export function HandoffModal({ conv, onClose, onCreated }: {
  conv?: Conversation;
  onClose: () => void;
  onCreated: (t: Task) => void;
}) {
  const [nodes, setNodes] = useState<NodeStatus[] | null>(null);
  const [presets, setPresets] = useState<AgentPreset[] | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [instruction, setInstruction] = useState("");
  const [choice, setChoice] = useState(""); // preset name or CUSTOM
  const [target, setTarget] = useState(""); // "node\u0000workspace"
  const [runtime, setRuntime] = useState<RuntimeName>("claude-code");
  const [ctxN, setCtxN] = useState(30);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const opKey = useRef<string | null>(null);
  const area = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    setTimeout(() => area.current?.focus(), 0);
    const fail = (e: unknown) => setLoadErr(e instanceof ApiError && e.status === 503 ? "未启用 Agent 协同（config.yaml 里配置 node）" : (e as Error).message);
    api.nodes().then(setNodes).catch((e) => { setNodes([]); fail(e); });
    api.agents().then(setPresets).catch(() => setPresets([]));
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Default: first available preset, otherwise custom.
  useEffect(() => {
    if (choice || presets === null) return;
    setChoice(presets.find((p) => p.available)?.name ?? CUSTOM);
  }, [presets, choice]);

  const options = useMemo(() => (nodes ?? []).filter((n) => n.online && n.info).flatMap((n) =>
    n.info!.workspaces.map((w) => ({ key: `${n.name}\u0000${w.name}`, node: n, ws: w }))), [nodes]);
  useEffect(() => { if (!target && options.length) setTarget(options[0].key); }, [options, target]);
  const chosen = options.find((o) => o.key === target);
  const runtimes = (chosen?.node.info?.runtimes ?? []).filter((r) => !r.error);
  useEffect(() => {
    if (runtimes.length && !runtimes.some((r) => r.name === runtime)) setRuntime(runtimes[0].name);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target, nodes]);

  const custom = choice === CUSTOM;
  const preset = presets?.find((p) => p.name === choice);
  const effRuntime = custom ? runtime : preset?.runtime;
  const ready = custom ? !!chosen : !!preset?.available;

  // A changed form is a new intent → new idempotency key.
  useEffect(() => { opKey.current = null; }, [instruction, choice, target, runtime, ctxN]);

  const submit = async () => {
    if (!ready || !instruction.trim() || busy) return;
    opKey.current ??= newOpKey();
    setBusy(true); setErr("");
    try {
      const base = { instruction: instruction.trim(), ...(conv ? { conversation_id: conv.id, context_messages: ctxN } : {}) };
      const t = await api.createTask(custom
        ? { ...base, node: chosen!.node.name, workspace: chosen!.ws.name, runtime }
        : { ...base, agent: preset!.name }, opKey.current);
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
  const title = conv ? "交给 Agent" : "新建任务";
  return (
    <div className="modal-scrim" onMouseDown={onClose}>
      <div className="modal handoff" role="dialog" aria-label={title} onMouseDown={(e) => e.stopPropagation()}>
        <div className="handoff-head">
          {conv ? <Bot size={16} /> : <Plus size={16} />} <span>{title}</span>
          {conv && <span className="muted handoff-src">· {conv.title}</span>}
          <button className="handoff-x" onClick={onClose} aria-label="关闭"><X size={16} /></button>
        </div>
        <div className="handoff-body">
          <textarea ref={area} rows={4} value={instruction} onChange={(e) => setInstruction(e.target.value)}
            placeholder={conv ? "帮我排查这个告警 / 按群里的需求改代码并起草回复" : "要 Agent 做什么？例如：给 README 补一段快速开始"}
            onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); submit(); } }} />

          {presets && presets.length > 0 && (
            <div className="preset-list" role="radiogroup" aria-label="Agent">
              {presets.map((p) => (
                <button key={p.name} role="radio" aria-checked={choice === p.name} disabled={!p.available}
                  className={`preset${choice === p.name ? " is-on" : ""}`} onClick={() => setChoice(p.name)}
                  title={p.available ? p.instructions || undefined : p.reason}>
                  <span className="preset-title">{p.title || p.name}</span>
                  <span className="preset-meta">
                    {p.available ? `${p.node} · ${p.workspace} · ${RUNTIME_LABEL[p.runtime] ?? p.runtime}` : p.reason}
                  </span>
                </button>
              ))}
              <button role="radio" aria-checked={custom} className={`preset${custom ? " is-on" : ""}`} onClick={() => setChoice(CUSTOM)}>
                <span className="preset-title">自定义</span>
                <span className="preset-meta">自选节点、工作区和运行时</span>
              </button>
            </div>
          )}

          {(custom || conv) && (
            <div className={`handoff-grid${conv ? "" : " no-ctx"}${custom ? "" : " ctx-only"}`}>
              {custom && (
                <>
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
                </>
              )}
              {conv && (
                <label>
                  <span>带上最近消息</span>
                  <input type="number" min={0} max={200} value={ctxN} onChange={(e) => setCtxN(Math.max(0, Math.min(200, Number(e.target.value) || 0)))} />
                </label>
              )}
            </div>
          )}
          <p className="handoff-hint">
            {effRuntime === "codex"
              ? "Codex 在 workspace-write 沙箱里运行（只能写任务工作区、默认不联网），不逐步审批。"
              : "有副作用的操作会推送审批；只读工具、工作区内改文件、白名单命令自动放行。"}
            {conv ? " 会话内容会作为上下文发给模型。" : ""}
          </p>
          {(loadErr || err) && <div className="composer-err handoff-err">{loadErr || err}</div>}
        </div>
        <div className="handoff-foot">
          <button className="btn btn-ghost" onClick={onClose}>取消</button>
          <button className="btn btn-primary" disabled={!ready || !instruction.trim() || busy} onClick={submit}
            title={!ready ? (custom ? "没有在线节点" : preset?.reason ?? "选择一个 Agent") : "⌘ Enter"}>
            {busy ? "创建中…" : conv ? "交给 Agent" : "创建"}
          </button>
        </div>
      </div>
    </div>
  );
}
