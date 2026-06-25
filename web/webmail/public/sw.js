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
      url: self.registration.scope + (data.message_id ? `#message/${data.message_id}` : "#inbox"),
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
  const url = event.notification.data?.url || self.registration.scope;
  event.waitUntil(
    clients.matchAll({ type: "window", includeUncontrolled: true }).then(windowClients => {
      for (const client of windowClients) {
        if (client.url.includes(self.registration.scope) && "focus" in client) {
          client.postMessage({ type: "notification-click", data: event.notification.data });
          return client.focus();
        }
      }
      if (clients.openWindow) {
        return clients.openWindow(url);
      }
    })
  );
});
