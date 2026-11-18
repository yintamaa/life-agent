import { Search } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { PlatformMark } from "../platform";
import { listTime } from "../lib/time";
import type { SearchHit } from "../types";

export function SearchModal({ open, onClose, onPick }: { open: boolean; onClose: () => void; onPick: (h: SearchHit) => void }) {
  const [q, setQ] = useState("");
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [busy, setBusy] = useState(false);
  const [cursor, setCursor] = useState(0);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => { if (open) { setTimeout(() => input.current?.focus(), 0); } else { setQ(""); setHits([]); } }, [open]);

  useEffect(() => {
    const t = q.trim();
    if (!t) { setHits([]); return; }
    let live = true;
    setBusy(true);
    const h = setTimeout(() => {
      api.search(t).then((r) => { if (live) { setHits(r); setCursor(0); } }).catch(() => live && setHits([])).finally(() => live && setBusy(false));
    }, 200);
    return () => { live = false; clearTimeout(h); };
  }, [q]);

  if (!open) return null;
  return (
    <div className="modal-scrim" onMouseDown={onClose}>
      <div className="modal" role="dialog" aria-label="搜索消息" onMouseDown={(e) => e.stopPropagation()}>
        <div className="modal-input">
          <Search size={16} className="muted" />
          <input
            ref={input}
            value={q}
            placeholder="搜索所有渠道的消息、联系人、群名…"
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") onClose();
              if (e.key === "ArrowDown") { e.preventDefault(); setCursor((c) => Math.min(c + 1, hits.length - 1)); }
              if (e.key === "ArrowUp") { e.preventDefault(); setCursor((c) => Math.max(c - 1, 0)); }
              if (e.key === "Enter" && hits[cursor]) onPick(hits[cursor]);
            }}
          />
          <kbd className="nav-kbd">Esc</kbd>
        </div>
        <ul className="modal-results">
          {!q.trim() && <li className="modal-hint">输入关键词，跨微信 / 飞书 / QQ / 邮件全文搜索</li>}
          {q.trim() && !busy && hits.length === 0 && <li className="modal-hint">没有找到“{q}”</li>}
          {hits.map((h, i) => (
            <li key={h.id}>
              <button className={`hit${i === cursor ? " is-cursor" : ""}`} onMouseEnter={() => setCursor(i)} onClick={() => onPick(h)}>
                <PlatformMark platform={h.platform} size={16} />
                <span className="hit-main">
                  <span className="hit-line1"><b>{h.conversation_title}</b><span className="muted"> · {h.sender_name}</span></span>
                  <span className="hit-text">{h.subject ? `${h.subject} — ` : ""}{h.text}</span>
                </span>
                <span className="conv-time">{listTime(h.sent_at)}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
