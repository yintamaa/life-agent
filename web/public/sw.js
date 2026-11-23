// life-agent service worker: Web Push only (no offline caching — keep it small).
// Payload: {title, body, tag?, url?, kind, data?: {approval_id?, task_id?}, actions?}

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

self.addEventListener("push", (event) => {
  let p = {};
  try { p = event.data ? event.data.json() : {}; } catch { p = { title: "life-agent", body: event.data && event.data.text() }; }
  const data = Object.assign({}, p.data || {}, { url: p.url || "./#/todo", kind: p.kind || "" });
  event.waitUntil(self.registration.showNotification(p.title || "life-agent", {
    body: p.body || "",
    tag: p.tag,
    renotify: p.kind === "approval",
    requireInteraction: p.kind === "approval",
    icon: "./icon-192.png",
    badge: "./icon-192.png",
    data,
    actions: Array.isArray(p.actions) ? p.actions.slice(0, 2) : [],
  }));
});

async function decide(id, allow) {
  let msg = allow ? "已允许" : "已拒绝";
  try {
    const r = await fetch(`./api/approvals/${encodeURIComponent(id)}`, {
      method: "POST", credentials: "same-origin",
      headers: { "Content-Type": "application/json" }, body: JSON.stringify({ allow }),
    });
    if (r.status === 409) {
      const b = await r.json().catch(() => ({}));
      const s = b.approval && b.approval.status;
      msg = `已在其他设备处理${s === "allowed" ? "：允许" : s === "denied" ? "：拒绝" : ""}`;
    } else if (r.status === 410) msg = "已过期";
    else if (!r.ok) msg = `操作失败（${r.status}）`;
  } catch { msg = "网络错误，请打开 life-agent 处理"; }
  await self.registration.showNotification("life-agent", { body: msg, tag: `decided-${id}` });
}

async function focusOrOpen(url) {
  const target = new URL(url, self.registration.scope).href;
  const wins = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const w of wins) {
    if (new URL(w.url).origin === new URL(target).origin) {
      await w.focus();
      if ("navigate" in w) return w.navigate(target);
      return;
    }
  }
  return self.clients.openWindow(target);
}

self.addEventListener("notificationclick", (event) => {
  const n = event.notification;
  const d = n.data || {};
  n.close();
  if ((event.action === "allow" || event.action === "deny") && d.approval_id) {
    event.waitUntil(decide(d.approval_id, event.action === "allow"));
  } else {
    event.waitUntil(focusOrOpen(d.url || "./#/todo"));
  }
});

// Best effort: the browser rotated the subscription — re-register it with the hub.
self.addEventListener("pushsubscriptionchange", (event) => {
  event.waitUntil((async () => {
    const old = event.oldSubscription;
    const r = await fetch("./api/push/vapid");
    if (!r.ok) return;
    const { public_key } = await r.json();
    const pad = "=".repeat((4 - (public_key.length % 4)) % 4);
    const key = Uint8Array.from(atob((public_key + pad).replace(/-/g, "+").replace(/_/g, "/")), (c) => c.charCodeAt(0));
    const sub = event.newSubscription || await self.registration.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
    await fetch("./api/push/subscribe", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(sub.toJSON()) });
    if (old) await fetch("./api/push/unsubscribe", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ endpoint: old.endpoint }) });
  })());
});
