import { AtSign, BellRing } from "lucide-react";
import { Avatar } from "./Avatar";
import { listTime } from "../lib/time";
import type { Conversation } from "../types";

export function ConversationList({ items, selected, onSelect, loading, emptyText }: {
  items: Conversation[];
  selected?: string;
  onSelect: (c: Conversation) => void;
  loading: boolean;
  emptyText: string;
}) {
  if (!items.length) {
    return <div className="list-empty">{loading ? "加载中…" : emptyText}</div>;
  }
  return (
    <ul className="conv-list" role="listbox" aria-label="会话">
      {items.map((c) => {
        const preview = c.kind === "group" && c.last_sender ? `${c.last_sender}：${c.last_preview}` : c.last_preview;
        return (
          <li key={c.id}>
            <button
              role="option"
              aria-selected={selected === c.id}
              className={`conv-row${selected === c.id ? " is-selected" : ""}${c.unread > 0 ? " is-unread" : ""}`}
              onClick={() => onSelect(c)}
            >
              <Avatar title={c.title} src={c.avatar} platform={c.platform} />
              <span className="conv-main">
                <span className="conv-line1">
                  <span className="conv-title">{c.title}</span>
                  {c.is_alert && <BellRing size={12} className="conv-alert" />}
                  <span className="conv-time">{listTime(c.last_message_at)}</span>
                </span>
                <span className="conv-line2">
                  {c.mentions > 0 && <span className="conv-at"><AtSign size={11} strokeWidth={2.4} />我</span>}
                  <span className="conv-preview">{preview}</span>
                  {c.unread > 0 && <span className="conv-unread">{c.unread > 99 ? "99+" : c.unread}</span>}
                </span>
              </span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
