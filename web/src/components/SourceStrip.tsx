import { useEffect, useState } from "react";
import { PlatformMark, STATE_LABEL } from "../platform";
import { relTime } from "../lib/time";
import type { Source } from "../types";

export function SourceStrip({ sources, active, onPick }: { sources: Source[]; active?: string; onPick: (id?: string) => void }) {
  const [, tick] = useState(0);
  useEffect(() => {
    const t = setInterval(() => tick((n) => n + 1), 1000);
    return () => clearInterval(t);
  }, []);

  if (!sources.length) return <div className="source-strip muted">还没有配置任何渠道</div>;

  return (
    <div className="source-strip" role="toolbar" aria-label="渠道状态">
      {sources.map((s) => {
        const isActive = active === s.id;
        const tip = s.state === "error" && s.error ? s.error : `${s.name} · ${STATE_LABEL[s.state]}${s.last_sync_at ? ` · 上次同步 ${new Date(s.last_sync_at).toLocaleString("zh-CN")}` : ""}`;
        return (
          <button
            key={s.id}
            className={`source-pill state-${s.state}${isActive ? " is-active" : ""}`}
            title={tip}
            aria-pressed={isActive}
            onClick={() => onPick(isActive ? undefined : s.id)}
          >
            <PlatformMark platform={s.platform} size={14} />
            <span className="source-name">{s.name}</span>
            <span className="source-dot" />
            <span className="source-state">{STATE_LABEL[s.state]}</span>
            {s.state !== "disabled" && (
              <span className="source-sync">· {s.state === "error" ? "上次 " : "同步 "}{relTime(s.last_sync_at)}</span>
            )}
          </button>
        );
      })}
      {active && <button className="source-clear" onClick={() => onPick(undefined)}>清除筛选</button>}
    </div>
  );
}
