import { mock } from "./mock";
import type { Conversation, Counts, Message, SearchHit, Source, View } from "./types";

let useMock = import.meta.env.VITE_MOCK === "1";
let everOk = false;
export const isMock = () => useMock;

const enc = encodeURIComponent;

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, init);
  if (!r.ok) {
    let msg = `${r.status}`;
    try { msg = (await r.json()).error ?? msg; } catch { /* ignore */ }
    throw new ApiError(r.status, msg);
  }
  if (r.status === 204) return undefined as T;
  return r.json() as Promise<T>;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) { super(message); }
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
    if (everOk || (e instanceof ApiError && e.status < 500)) throw e;
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
};

/**
 * Subscribe to backend change events. Returns unsubscribe. No-op in mock mode.
 * Each event carries the hub's change seq as its SSE id; on reconnect the
 * browser sends Last-Event-ID and the hub immediately reports anything missed.
 */
export function subscribe(onChange: () => void): () => void {
  if (useMock || typeof EventSource === "undefined") return () => {};
  const es = new EventSource("/api/events");
  es.addEventListener("changed", onChange);
  return () => es.close();
}
