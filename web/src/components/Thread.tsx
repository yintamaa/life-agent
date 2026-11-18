import { Check, ExternalLink, FileText, Image as ImageIcon, Link2, Mic, SendHorizontal, Video } from "lucide-react";
import { Fragment, useEffect, useRef, useState } from "react";
import { api, ApiError } from "../api";
import { PLATFORM } from "../platform";
import { clockTime, dayLabel, sameDay } from "../lib/time";
import type { Conversation, Message } from "../types";

const TYPE_ICON: Partial<Record<Message["type"], typeof FileText>> = {
  image: ImageIcon, file: FileText, voice: Mic, video: Video, link: Link2,
};

export function Thread({ conv, version, onDone, onSent }: {
  conv: Conversation;
  version: number;
  onDone: (c: Conversation) => void;
  onSent: () => void;
}) {
  const [msgs, setMsgs] = useState<Message[]>([]);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState("");
  const scroller = useRef<HTMLDivElement>(null);
  const lastConv = useRef<string>("");

  useEffect(() => {
    let live = true;
    const switched = lastConv.current !== conv.id;
    lastConv.current = conv.id;
    if (switched) { setLoading(true); setMsgs([]); }
    api.messages(conv.id)
      .then((m) => { if (live) { setMsgs(m); setErr(""); } })
      .catch((e) => live && setErr(String(e.message ?? e)))
      .finally(() => live && setLoading(false));
    return () => { live = false; };
  }, [conv.id, version]);

  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [msgs]);

  const isGroup = conv.kind === "group";
  const p = PLATFORM[conv.platform];

  return (
    <section className="thread" aria-label={conv.title}>
      <header className="thread-head">
        <div className="thread-title">
          <span className="thread-name">{conv.title}</span>
          <span className="thread-meta">
            <span className="thread-chip" style={{ color: p.color }}>{p.label}</span>
            {conv.kind === "group" ? "群聊" : conv.kind === "p2p" ? "私聊" : "邮件"}
            {conv.source.includes(":") ? ` · ${conv.source.split(":")[1]}` : ""}
          </span>
        </div>
        <div className="thread-actions">
          {conv.needs_attention && (
            <button className="btn btn-ghost" onClick={() => onDone(conv)} title="从“需要看”中移除">
              <Check size={14} /> 标记已处理
            </button>
          )}
        </div>
      </header>

      <div className="thread-body" ref={scroller}>
        {loading && <div className="thread-empty">加载中…</div>}
        {!loading && err && <div className="thread-empty error">加载失败：{err}</div>}
        {!loading && !err && msgs.length === 0 && <div className="thread-empty">暂无消息</div>}
        {msgs.map((m, i) => {
          const prev = msgs[i - 1];
          const newDay = !prev || !sameDay(prev.sent_at, m.sent_at);
          const grouped = !newDay && prev && prev.sender_id === m.sender_id && prev.is_self === m.is_self &&
            new Date(m.sent_at).getTime() - new Date(prev.sent_at).getTime() < 5 * 60_000;
          return (
            <Fragment key={m.id}>
              {newDay && <div className="day-sep"><span>{dayLabel(m.sent_at)}</span></div>}
              <MessageRow m={m} showName={isGroup && !m.is_self && !grouped} compact={!!grouped} />
            </Fragment>
          );
        })}
      </div>

      <Composer conv={conv} onSent={() => { onSent(); api.messages(conv.id).then(setMsgs).catch(() => {}); }} />
    </section>
  );
}

function MessageRow({ m, showName, compact }: { m: Message; showName: boolean; compact: boolean }) {
  if (m.type === "system") return <div className="msg-system">{m.text}</div>;
  const Icon = TYPE_ICON[m.type];
  return (
    <div className={`msg${m.is_self ? " is-self" : ""}${compact ? " is-compact" : ""}`}>
      <div className="msg-col">
        {showName && <div className="msg-name">{m.sender_name}</div>}
        <div className={`bubble type-${m.type}${m.mentions_me ? " is-mention" : ""}`}>
          {m.subject && <div className="bubble-subject">{m.subject}</div>}
          {Icon && <Icon size={14} className="bubble-icon" />}
          <span className="bubble-text">{m.text}</span>
        </div>
      </div>
      <time className="msg-time" dateTime={m.sent_at}>{clockTime(m.sent_at)}</time>
    </div>
  );
}

function Composer({ conv, onSent }: { conv: Conversation; onSent: () => void }) {
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => { setText(""); setErr(""); }, [conv.id]);

  const disabled = !conv.can_send;
  const submit = async () => {
    const t = text.trim();
    if (!t || busy || disabled) return;
    setBusy(true); setErr("");
    try {
      await api.send(conv.id, t);
      setText("");
      onSent();
    } catch (e) {
      setErr(e instanceof ApiError && e.status === 501 ? "该渠道暂不支持发送" : `发送失败：${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className={`composer${disabled ? " is-disabled" : ""}`} title={disabled ? "该渠道暂不支持发送" : undefined}>
      <textarea
        rows={1}
        value={text}
        disabled={disabled}
        placeholder={disabled ? `该渠道暂不支持发送 · 请在${PLATFORM[conv.platform].label}客户端回复` : `回复 ${conv.title}（Enter 发送，Shift+Enter 换行）`}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); }
        }}
      />
      {err && <span className="composer-err">{err}</span>}
      {disabled ? (
        <span className="composer-hint"><ExternalLink size={13} /> 只读</span>
      ) : (
        <button className="btn btn-primary" disabled={!text.trim() || busy} onClick={submit} aria-label="发送">
          <SendHorizontal size={14} />
        </button>
      )}
    </div>
  );
}
