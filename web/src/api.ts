import { mock } from "./mock";
import type {
  AgentPreset, Approval, Conversation, Counts, CreateTaskReq, Message, NodeStatus, SearchHit, Source, Task, TaskEvent, View,
} from "./types";

let useMock = import.meta.env.VITE_MOCK === "1";
let everOk = false;
export const isMock = () => useMock;

const enc = encodeURIComponent;

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, init);
  if (!r.ok) {
    let msg = `${r.status}`;
    let body: unknown;
    try { body = await r.json(); msg = (body as { error?: string }).error ?? msg; } catch { /* ignore */ }
    throw new ApiError(r.status, msg, body);
  }
  if (r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}

export class ApiError extends Error {
  constructor(public status: number, message: string, public body?: unknown) { super(message); }
}

/** Try the real API; if it is unreachable (network error / proxy 5xx with no backend), flip to mock mode. */
async function call<T>(real: () => Promise<T>, fake: () => T): Promise<T> {
  if (useMock) return fake();
  try {
    const v = await real();
    everOk = true;
    return v;
  } catch (e) {
    // Only fall back before the backend has ever answered; afterwards surface real errors.
    // 503 is a real answer (e.g. agent orchestration disabled), not "backend down".
    if (everOk || (e instanceof ApiError && (e.status < 500 || e.status === 503))) throw e;
    console.warn("[life-agent] API 不可用，切换到 mock 数据", e);
    useMock = true;
    return fake();
  }
}

const post = (path: string, body?: unknown, headers: Record<string, string> = {}) =>
  req<void>(path, {
    method: "POST",
    headers: body ? { "Content-Type": "application/json", ...headers } : headers,
    body: body ? JSON.stringify(body) : undefined,
  });

const postJSON = <T>(path: string, body: unknown, headers: Record<string, string> = {}) =>
  req<T>(path, { method: "POST", headers: { "Content-Type": "application/json", ...headers }, body: JSON.stringify(body) });

/** A fresh idempotency key; reuse the same key only for automatic retries of one send. */
export const newOpKey = () =>
  (globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`);

export const api = {
  sources: () => call(() => req<Source[]>("/api/sources"), mock.sources),
  counts: () => call(() => req<Counts>("/api/counts"), mock.counts),
  conversations: (view: View, source?: string) =>
    call(
      () => req<Conversation[]>(`/api/conversations?view=${view}&limit=50${source ? `&source=${enc(source)}` : ""}`),
      () => mock.conversations(view, source),
    ),
  messages: (id: string) => call(() => req<Message[]>(`/api/conversations/${enc(id)}/messages?limit=50`), () => mock.messages(id)),
  /** seenAt = newest message the user had on screen; newer messages stay unread. */
  read: (id: string, seenAt?: string) =>
    call(() => post(`/api/conversations/${enc(id)}/read`, seenAt ? { seen_at: seenAt } : undefined), () => mock.read(id)),
  /** Rejects with ApiError 409 if messages arrived after seenAt (they won't be dismissed). */
  done: (id: string, seenAt?: string) =>
    call(() => post(`/api/conversations/${enc(id)}/done`, seenAt ? { seen_at: seenAt } : undefined), () => mock.done(id)),
  send: (id: string, text: string, opKey: string) =>
    call(() => post(`/api/conversations/${enc(id)}/send`, { text }, { "Idempotency-Key": opKey }), () => mock.send(id, text)),
  search: (q: string) => call(() => req<SearchHit[]>(`/api/search?q=${enc(q)}&limit=50`), () => mock.search(q)),

  // ---- agent orchestration ----
  nodes: () => call(() => req<NodeStatus[]>("/api/nodes"), mock.nodes),
  agents: () => call(() => req<AgentPreset[]>("/api/agents"), mock.agents),
  tasks: (status: string) => call(() => req<Task[]>(`/api/tasks?status=${enc(status)}&limit=100`), () => mock.tasks(status)),
  task: (id: string) => call(() => req<Task>(`/api/tasks/${enc(id)}`), () => mock.task(id)),
  /** Incremental: only events with seq > after. */
  taskEvents: (id: string, after: number) =>
    call(() => req<TaskEvent[]>(`/api/tasks/${enc(id)}/events?after=${after}`), () => mock.taskEvents(id, after)),
  /** opKey: keep the same key across automatic retries of one submit. */
  createTask: (body: CreateTaskReq, opKey: string) =>
    call(() => postJSON<Task>("/api/tasks", body, { "Idempotency-Key": opKey }), () => mock.createTask(body)),
  taskInput: (id: string, text: string) => call(() => post(`/api/tasks/${enc(id)}/input`, { text }), () => mock.taskInput(id, text)),
  taskCancel: (id: string) => call(() => post(`/api/tasks/${enc(id)}/cancel`), () => mock.taskCancel(id)),
  taskClose: (id: string) => call(() => post(`/api/tasks/${enc(id)}/close`), () => mock.taskClose(id)),
  approvals: (pendingOnly: boolean, taskId?: string) =>
    call(
      () => req<Approval[]>(`/api/approvals?pending=${pendingOnly ? 1 : 0}${taskId ? `&task_id=${enc(taskId)}` : ""}`),
      () => mock.approvals(pendingOnly, taskId),
    ),
  /**
   * First decision across devices wins. Resolves with the effective approval;
   * `conflict` is "already" when another device decided first, "expired" when
   * the agent stopped waiting.
   */
  decide: (id: string, allow: boolean): Promise<{ approval: Approval; conflict?: "already" | "expired" }> =>
    call(
      async () => {
        try {
          return { approval: await postJSON<Approval>(`/api/approvals/${enc(id)}`, { allow }) };
        } catch (e) {
          if (e instanceof ApiError && (e.status === 409 || e.status === 410)) {
            const a = (e.body as { approval?: Approval } | undefined)?.approval;
            if (a) return { approval: a, conflict: e.status === 409 ? "already" as const : "expired" as const };
          }
          throw e;
        }
      },
      () => mock.decide(id, allow),
    ),
};

// ---- web push (never mocked: needs a real backend + service worker) ----
export const pushApi = {
  vapid: () => req<{ public_key: string }>("/api/push/vapid"),
  subscribe: (sub: PushSubscriptionJSON) => postJSON<void>("/api/push/subscribe", sub),
  unsubscribe: (endpoint: string) => postJSON<void>("/api/push/unsubscribe", { endpoint }),
  test: () => postJSON<{ delivered: number }>("/api/push/test", {}),
};

/**
 * Subscribe to backend change events. Returns unsubscribe. No-op in mock mode.
 * Each event carries the hub's change seq as its SSE id; on reconnect the
 * browser sends Last-Event-ID and the hub immediately reports anything missed.
 */
let es: EventSource | null = null;
const listeners = new Set<() => void>();

export function subscribe(onChange: () => void): () => void {
  if (useMock || typeof EventSource === "undefined") return () => {};
  listeners.add(onChange);
  if (!es) {
    es = new EventSource("/api/events");
    es.addEventListener("changed", () => listeners.forEach((l) => l()));
  }
  return () => {
    listeners.delete(onChange);
    if (listeners.size === 0 && es) { es.close(); es = null; }
  };
}
