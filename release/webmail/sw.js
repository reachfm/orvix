/* Orvix Webmail — service worker.
 *
 * Registered from release/webmail/assets/webmail-push.js via
 *   navigator.serviceWorker.register('/webmail/sw.js', { scope: '/webmail/' })
 *
 * Scope is /webmail/, which works for BOTH:
 *   https://webmail.<domain>/webmail/  (dedicated hostname)
 *   https://admin.<domain>/webmail/    (admin hostname serving webmail SPA)
 *
 * The icon + badge paths use absolute paths so the browser
 * fetches them from the same origin regardless of scope. The
 * /assets/ prefix is rewritten to /webmail/assets/ by the SPA
 * wildcard route at the backend — see internal/api/router.go
 * serveSPA — so the file resolves to release/webmail/assets/icon-192.png
 * in both cases. The bundled webmail-push.js publishes
 * /webmail/assets/icon-192.png alongside /webmail/assets/webmail.js.
 */

self.addEventListener("push", event => {
  let data = { title: "New mail received", body: "You have a new message." };
  try {
    if (event.data) {
      data = event.data.json();
    }
  } catch (_) { }
  const promise = self.registration.showNotification(data.title || "New mail received", {
    body: data.body || "You have a new message.",
    icon: "/webmail/assets/icon-192.png",
    badge: "/webmail/assets/icon-192.png",
    data: {
      // The notification click handler routes the user into the
      // SPA. The hash route /#inbox is honoured by the
      // hash-based router in webmail.js — see routeFromHash.
      // If message_id is present we route to that specific
      // message once the SPA has loaded.
      url: (self.registration.scope || "/webmail/") + (data.message_id ? "#message/" + data.message_id : "#inbox"),
      message_id: data.message_id,
      mailbox_id: data.mailbox_id,
    },
    requireInteraction: false,
    tag: data.message_id || "orvix-new-mail",
  });
  event.waitUntil(promise);
});

self.addEventListener("notificationclick", event => {
  event.notification.close();
  const url = event.notification.data?.url || (self.registration.scope || "/webmail/");
  event.waitUntil(
    clients.matchAll({ type: "window", includeUncontrolled: true }).then(windowClients => {
      for (const client of windowClients) {
        if (client.url.includes(self.registration.scope || "/webmail/") && "focus" in client) {
          // Reuse the existing tab if it is open. We also
          // postMessage so the SPA can navigate to the
          // specific message (the click hash above may not
          // match the current location, in which case the
          // SPA will silently ignore it).
          try {
            client.postMessage({
              type: "notification-click",
              data: event.notification.data || {},
            });
          } catch (_) { /* postMessage can fail across origins — fall through to focus */ }
          return client.focus();
        }
      }
      if (clients.openWindow) {
        return clients.openWindow(url);
      }
    })
  );
});

// Chrome (and Firefox in some configurations) auto-rotate the
// push subscription. When that happens, the browser fires
// `pushsubscriptionchange` on the service worker. Without a
// handler here the user's subscription expires silently and
// they stop receiving push notifications.
//
// The standard remediation is to re-subscribe with the same
// VAPID key. We don't have access to the VAPID key inside the
// SW — it's owned by the page-side script — so we postMessage
// any open client to ask it to re-subscribe. If no client is
// open, the user will be re-prompted the next time they load
// the webmail SPA, which calls webmail-push.js's
// resubscribeFromCurrentState() when it sees the message.
self.addEventListener("pushsubscriptionchange", event => {
  event.waitUntil(
    clients.matchAll({ type: "window", includeUncontrolled: true }).then(windowClients => {
      for (const client of windowClients) {
        try {
          client.postMessage({
            type: "pushsubscriptionchange",
            oldEndpoint: (event.oldSubscription && event.oldSubscription.endpoint) || null,
            newEndpoint: (event.newSubscription && event.newSubscription.endpoint) || null,
          });
        } catch (_) { /* cross-origin client — skip */ }
      }
      // No client? Unregister so a fresh register() on next load
      // gets a clean PushManager state. The browser GC will
      // reclaim this SW within a few minutes if nothing
      // re-registers; doing it explicitly avoids surprising
      // "registered but inactive" entries in chrome://serviceworker-internals.
      return self.registration.unregister().catch(() => null);
    })
  );
});