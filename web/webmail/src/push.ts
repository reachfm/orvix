const VAPID_PUBKEY_URL = "/api/v1/webmail/push/status";

let swRegistration: ServiceWorkerRegistration | null = null;

function urlBase64ToUint8Array(base64: string) {
  const padding = "=".repeat((4 - (base64.length % 4)) % 4);
  const raw = window.atob((base64 + padding).replace(/-/g, "+").replace(/_/g, "/"));
  const output = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) output[i] = raw.charCodeAt(i);
  return output;
}

export async function registerServiceWorker() {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) return null;
  try {
    swRegistration = await navigator.serviceWorker.register("/sw.js", { scope: "/" });
    return swRegistration;
  } catch (e) {
    console.error("Service worker registration failed", e);
    return null;
  }
}

export async function requestPushSubscription(): Promise<PushSubscriptionJSON | null> {
  if (!swRegistration) return null;
  const statusResp = await fetch(VAPID_PUBKEY_URL, { credentials: "include" });
  if (!statusResp.ok) return null;
  const status = await statusResp.json();
  if (!status.enabled || !status.vapid_public_key) return null;
  const vapidKey = urlBase64ToUint8Array(status.vapid_public_key);
  try {
    const sub = await swRegistration.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: vapidKey,
    });
    const subJSON = sub.toJSON();
    const resp = await fetch("/api/v1/webmail/push/subscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify({
        endpoint: subJSON.endpoint,
        keys: { p256dh: subJSON.keys?.p256dh, auth: subJSON.keys?.auth },
      }),
    });
    if (!resp.ok) throw new Error("Subscribe failed: " + resp.status);
    return subJSON;
  } catch (e) {
    console.error("Push subscription failed", e);
    return null;
  }
}

export async function unsubscribePush(endpoint: string) {
  await fetch("/api/v1/webmail/push/unsubscribe", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ endpoint }),
  });
}

export async function testPushNotification(endpoint: string) {
  const resp = await fetch("/api/v1/webmail/push/test", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    credentials: "include",
    body: JSON.stringify({ endpoint }),
  });
  return resp.ok;
}

export function isPushSupported() {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window;
}

export async function hasNotificationPermission() {
  if (!("Notification" in window)) return false;
  if (Notification.permission === "granted") return true;
  if (Notification.permission === "denied") return false;
  const result = await Notification.requestPermission();
  return result === "granted";
}
