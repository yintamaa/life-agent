import type { TaskStatus } from "../types";

export const STATUS: Record<TaskStatus, { label: string; tone: "run" | "warn" | "ok" | "err" | "off" }> = {
  queued: { label: "排队中", tone: "off" },
  running: { label: "运行中", tone: "run" },
  waiting_approval: { label: "待审批", tone: "warn" },
  idle: { label: "本轮完成", tone: "ok" },
  failed: { label: "失败", tone: "err" },
  cancelled: { label: "已取消", tone: "off" },
  done: { label: "已关闭", tone: "off" },
};

export const RUNTIME_LABEL: Record<string, string> = { "claude-code": "Claude Code", codex: "Codex" };

export const canContinue = (s: TaskStatus) => s === "idle" || s === "failed" || s === "cancelled";
export const isLive = (s: TaskStatus) => s === "running" || s === "waiting_approval" || s === "queued";

/** Extract the agent's suggested reply from a ```reply fenced block. */
export function extractReply(result: string): string | null {
  const m = /```reply[ \t]*\r?\n([\s\S]*?)```/.exec(result);
  return m ? m[1].trim() : null;
}

export type Segment = { kind: "text"; text: string } | { kind: "code"; lang: string; text: string };

/** Split markdown-ish text into plain text and ``` fenced code segments. */
export function splitFences(src: string): Segment[] {
  const out: Segment[] = [];
  const re = /```([\w-]*)[ \t]*\r?\n([\s\S]*?)(?:```|$)/g;
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = re.exec(src))) {
    if (m.index > last) out.push({ kind: "text", text: src.slice(last, m.index) });
    out.push({ kind: "code", lang: m[1], text: m[2].replace(/\n$/, "") });
    last = re.lastIndex;
  }
  if (last < src.length) out.push({ kind: "text", text: src.slice(last) });
  return out.filter((s) => s.kind === "code" || s.text.trim() !== "");
}

/** "12:03" style countdown until iso; null when passed. */
export function countdown(iso: string, now = Date.now()): string | null {
  const ms = new Date(iso).getTime() - now;
  if (Number.isNaN(ms) || ms <= 0) return null;
  const s = Math.floor(ms / 1000);
  const m = Math.floor(s / 60);
  return `${m}:${String(s % 60).padStart(2, "0")}`;
}
