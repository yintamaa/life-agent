import { Bell, BellOff, BellRing } from "lucide-react";
import { useEffect, useState } from "react";
import { ApiError, isMock, pushApi } from "../api";
import { disablePush, enablePush, pushState, type PushState } from "../lib/push";

/** Compact per-device notification switch for the 待办 header. */
export function PushControl() {
  const [st, setSt] = useState<PushState | null>(null);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");

  useEffect(() => {
    if (isMock()) { setSt("unsupported"); return; }
    pushState().then(setSt).catch(() => setSt("unsupported"));
  }, []);

  const run = async (fn: () => Promise<PushState | void>, ok?: string) => {
    setBusy(true); setNote("");
    try {
      const next = await fn();
      if (next) setSt(next);
      if (ok) setNote(ok);
    } catch (e) {
      if (e instanceof ApiError && e.status === 503) setSt("off-server");
      else setNote((e as Error).message);
    } finally { setBusy(false); }
  };

  if (st === null || st === "unsupported") return null;
  return (
    <div className="push-ctl">
      {st === "on" ? (
        <>
          <span className="push-on"><BellRing size={13} /> 通知已开启</span>
          <button className="btn btn-ghost btn-sm" disabled={busy}
            onClick={() => run(async () => { const r = await pushApi.test(); setNote(r.delivered ? "已发送测试通知" : "没有送达任何设备"); })}>测试</button>
          <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => run(disablePush)}>关闭</button>
        </>
      ) : st === "default" ? (
        <button className="btn btn-ghost btn-sm" disabled={busy} onClick={() => run(enablePush, "审批和任务完成会通知这台设备")}>
          <Bell size={13} /> 开启通知
        </button>
      ) : st === "denied" ? (
        <span className="push-hint"><BellOff size={13} /> 通知被拒绝：在浏览器的网站设置里允许通知后刷新</span>
      ) : st === "needs-install" ? (
        <span className="push-hint"><Bell size={13} /> 在 Safari 点「分享 → 添加到主屏幕」，从主屏幕打开后才能开启通知</span>
      ) : (
        <span className="push-hint"><BellOff size={13} /> hub 未启用推送</span>
      )}
      {note && <span className="push-note">{note}</span>}
    </div>
  );
}
