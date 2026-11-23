import { pushApi } from "../api";

export type PushState = "unsupported" | "needs-install" | "default" | "denied" | "on" | "off-server";

const isIOS = () => /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
const standalone = () =>
  window.matchMedia?.("(display-mode: standalone)").matches || (navigator as Navigator & { standalone?: boolean }).standalone === true;

function urlBase64ToUint8Array(b64: string): Uint8Array {
  const pad = "=".repeat((4 - (b64.length % 4)) % 4);
  const raw = atob((b64 + pad).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

async function registration() {
  return (await navigator.serviceWorker.getRegistration("./")) ?? navigator.serviceWorker.register("./sw.js", { scope: "./" });
}

export async function pushState(): Promise<PushState> {
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
    return isIOS() && !standalone() ? "needs-install" : "unsupported";
  }
  if (isIOS() && !standalone()) return "needs-install";
  if (Notification.permission === "denied") return "denied";
  const sub = await (await registration()).pushManager.getSubscription();
  return sub ? "on" : "default";
}

/** Ask permission, subscribe this device, register it with the hub. */
export async function enablePush(): Promise<PushState> {
  const perm = await Notification.requestPermission();
  if (perm !== "granted") return perm === "denied" ? "denied" : "default";
  const { public_key } = await pushApi.vapid();
  const reg = await registration();
  const sub = (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: urlBase64ToUint8Array(public_key) as BufferSource }));
  await pushApi.subscribe(sub.toJSON());
  return "on";
}

export async function disablePush(): Promise<PushState> {
  const sub = await (await registration()).pushManager.getSubscription();
  if (sub) {
    await pushApi.unsubscribe(sub.endpoint).catch(() => {});
    await sub.unsubscribe();
  }
  return "default";
}
