import { AtSign, CircleDot, Hand, MessageSquareText, MessagesSquare, Siren, User, Users } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { LucideIcon } from "lucide-react";
import { api, ApiError, isMock, subscribe } from "../api";
import { ConversationList } from "./ConversationList";
import { SourceStrip } from "./SourceStrip";
import { Thread } from "./Thread";
import type { Conversation, Counts, Source, View } from "../types";

const TABS: Array<{ view: View; label: string; icon: LucideIcon; count?: boolean; tone?: string }> = [
  { view: "needs", label: "需要看", icon: Hand, count: true },
  { view: "all", label: "全部", icon: MessagesSquare },
  { view: "unread", label: "未读", icon: CircleDot },
  { view: "mention", label: "@我", icon: AtSign, count: true },
  { view: "p2p", label: "私聊", icon: User },
  { view: "group", label: "群聊", icon: Users },
  { view: "alert", label: "线上告警", icon: Siren, count: true, tone: "alert" },
];

const EMPTY: Record<View, string> = {
  needs: "没有需要处理的消息，清爽 ✓",
  all: "还没有同步到会话",
  unread: "全部已读",
  mention: "没有人 @ 你",
  p2p: "没有私聊",
  group: "没有群聊",
  alert: "没有线上告警，岁月静好",
};

const VIEWS: View[] = ["needs", "all", "unread", "mention", "p2p", "group", "alert"];
const params = new URLSearchParams(location.search);
const initialView = (VIEWS as string[]).includes(params.get("v") ?? "") ? (params.get("v") as View) : "needs";
const initialConv = params.get("c");

export function MessagesPage({ onCounts, jumpTo }: { onCounts: (c: Counts) => void; jumpTo?: { id: string; nonce: number } }) {
  const [view, setView] = useState<View>(initialView);
  const [source, setSource] = useState<string | undefined>();
  const [sources, setSources] = useState<Source[]>([]);
  const [counts, setCounts] = useState<Counts | null>(null);
  const [convs, setConvs] = useState<Conversation[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Conversation | null>(null);
  const [version, setVersion] = useState(0);
  const [mock, setMock] = useState(isMock());
  const [notice, setNotice] = useState("");
  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(""), 4000);
    return () => clearTimeout(t);
  }, [notice]);

  const loadSources = useCallback(() => api.sources().then(setSources).catch(() => {}), []);
  const loadCounts = useCallback(() => api.counts().then((c) => { setCounts(c); onCounts(c); }).catch(() => {}), [onCounts]);
  const loadList = useCallback(() => {
    return api.conversations(view, source)
      .then((list) => {
        setConvs(list);
        setSelected((cur) => (cur ? list.find((c) => c.id === cur.id) ?? cur : cur));
      })
      .catch(() => setConvs([]))
      .finally(() => { setLoading(false); setMock(isMock()); });
  }, [view, source]);

  useEffect(() => { setLoading(true); loadList(); }, [loadList]);

  // deep link: ?v=all&c=<conversation id> (opens once on first load)
  const deepLinked = useRef(false);
  useEffect(() => {
    if (deepLinked.current || !initialConv || !convs.length) return;
    deepLinked.current = true;
    const c = convs.find((x) => x.id === initialConv);
    if (c) open(c);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [convs]);
  useEffect(() => { loadSources(); loadCounts(); const t = setInterval(loadSources, 15_000); return () => clearInterval(t); }, [loadSources, loadCounts]);

  // live refresh from SSE, debounced
  const refreshAll = useRef<() => void>(() => {});
  refreshAll.current = () => { loadSources(); loadCounts(); loadList(); setVersion((v) => v + 1); };
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;
    const unsub = subscribe(() => { clearTimeout(timer); timer = setTimeout(() => refreshAll.current(), 500); });
    return () => { clearTimeout(timer); unsub(); };
  }, [mock]);

  // jump from global search
  useEffect(() => {
    if (!jumpTo) return;
    setView("all"); setSource(undefined);
    api.conversations("all").then((list) => {
      const c = list.find((x) => x.id === jumpTo.id);
      if (c) open(c);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [jumpTo?.nonce]);

  const open = (c: Conversation) => {
    setSelected(c);
    if (c.unread > 0 || c.mentions > 0) {
      api.read(c.id, c.last_message_at).then(() => {
        setConvs((list) => list.map((x) => (x.id === c.id ? { ...x, unread: 0, mentions: 0 } : x)));
        loadCounts();
      }).catch(() => {});
    }
  };

  const done = (c: Conversation, seenAt?: string) => {
    api.done(c.id, seenAt).then(() => {
      setSelected((cur) => (cur && cur.id === c.id ? { ...cur, needs_attention: false } : cur));
      if (view === "needs") setConvs((list) => list.filter((x) => x.id !== c.id));
      loadCounts();
    }).catch((e) => {
      if (e instanceof ApiError && e.status === 409) {
        setNotice("处理期间有新消息，已保留在「需要看」");
        refreshAll.current();
      } else {
        setNotice(`操作失败：${(e as Error).message}`);
      }
    });
  };

  return (
    <div className="page">
      <header className="page-head">
        <div className="page-title-row">
          <span className="page-icon"><MessageSquareText size={20} strokeWidth={1.8} /></span>
          <h1>消息</h1>
          {mock && <span className="mock-tag" title="后端未连接，正在展示示例数据">示例数据</span>}
        </div>
        <p className="page-sub">微信、飞书、QQ、邮件的会话在一处；线上告警单独一栏</p>
        {notice && <div className="notice" role="status">{notice}</div>}
        <div className="tabs" role="tablist">
          {TABS.map((t) => {
            const Icon = t.icon;
            const n = counts?.[t.view] ?? 0;
            return (
              <button key={t.view} role="tab" aria-selected={view === t.view}
                className={`tab${view === t.view ? " is-active" : ""}${t.tone === "alert" ? " tone-alert" : ""}`}
                onClick={() => { setView(t.view); }}>
                <Icon size={15} strokeWidth={1.9} />
                <span>{t.label}</span>
                {t.count && n > 0 ? <span className="tab-count">{n}</span> : null}
              </button>
            );
          })}
        </div>
      </header>

      <SourceStrip sources={sources} active={source} onPick={setSource} />

      <div className={`panes${selected ? " has-selection" : ""}`}>
        <div className="pane-list">
          <ConversationList items={convs} selected={selected?.id} onSelect={open} loading={loading} emptyText={EMPTY[view]} />
        </div>
        <div className="pane-thread">
          {selected ? (
            <>
              <button className="back-mobile" onClick={() => setSelected(null)}>← 返回</button>
              <Thread conv={selected} version={version} onDone={done} onSent={() => { loadList(); loadCounts(); }} />
            </>
          ) : (
            <div className="thread-placeholder">
              <MessagesSquare size={28} strokeWidth={1.4} />
              <span>选择一个会话</span>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
