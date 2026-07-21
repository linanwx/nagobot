// Web Push enrollment for this browser. The server holds the VAPID keypair
// and the subscription store; this module owns the browser side: service
// worker registration, permission, and the PushManager subscription.

function urlBase64ToUint8Array(base64: string): Uint8Array<ArrayBuffer> {
  const padding = "=".repeat((4 - (base64.length % 4)) % 4);
  const b64 = (base64 + padding).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b64);
  // Explicitly back the view with an ArrayBuffer — TS 5.7's BufferSource
  // rejects Uint8Array<ArrayBufferLike> (it could wrap a SharedArrayBuffer).
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

// Push needs a secure context, a service worker, and the Push API. iOS
// additionally requires the app to be installed to the Home Screen — in a
// plain Safari tab serviceWorker exists but PushManager does not, so this
// correctly reports unsupported there.
export function pushSupported(): boolean {
  return (
    window.isSecureContext &&
    "serviceWorker" in navigator &&
    "PushManager" in window &&
    "Notification" in window
  );
}

async function swRegistration(): Promise<ServiceWorkerRegistration> {
  const reg = await navigator.serviceWorker.register("/sw.js");
  await navigator.serviceWorker.ready;
  return reg;
}

// currentSubscription returns this browser's live push subscription, if any.
export async function currentSubscription(): Promise<PushSubscription | null> {
  if (!pushSupported()) return null;
  const reg = await navigator.serviceWorker.getRegistration();
  if (!reg) return null;
  return reg.pushManager.getSubscription();
}

// enablePush walks the full enrollment: permission → SW → subscribe → server.
export async function enablePush(): Promise<void> {
  if (!pushSupported()) {
    throw new Error("push not supported in this context");
  }
  const permission = await Notification.requestPermission();
  if (permission !== "granted") {
    throw new Error("notification permission denied");
  }

  const keyRes = await fetch("/api/push/vapid-key");
  if (!keyRes.ok) throw new Error(`GET /api/push/vapid-key: ${keyRes.status}`);
  const { key } = (await keyRes.json()) as { key: string };

  const reg = await swRegistration();
  const sub =
    (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(key),
    }));

  const res = await fetch("/api/push/subscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(sub.toJSON()),
  });
  if (!res.ok) throw new Error(`POST /api/push/subscribe: ${res.status}`);
}

// disablePush tears down both sides; server first so a failure leaves the
// browser subscription intact for a retry.
export async function disablePush(): Promise<void> {
  const sub = await currentSubscription();
  if (!sub) return;
  const res = await fetch("/api/push/unsubscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ endpoint: sub.endpoint }),
  });
  if (!res.ok) throw new Error(`POST /api/push/unsubscribe: ${res.status}`);
  await sub.unsubscribe();
}
