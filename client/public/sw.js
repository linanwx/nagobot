// nagobot service worker: Web Push display + click-through. Kept dependency
// free and cache free — the app itself stays network-served; this worker
// exists only so push notifications can be shown when no tab is open.

self.addEventListener("install", () => {
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(self.clients.claim());
});

self.addEventListener("push", (event) => {
  let data = {};
  try {
    data = event.data ? event.data.json() : {};
  } catch {
    data = { body: event.data ? event.data.text() : "" };
  }
  const title = data.title || "nagobot";
  event.waitUntil(
    self.registration.showNotification(title, {
      body: data.body || "",
      icon: "/icon-192.png",
      badge: "/icon-192.png",
      tag: data.session || "nagobot",
      data: { session: data.session || "" },
    }),
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(
    (async () => {
      const all = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });
      if (all.length > 0) {
        await all[0].focus();
        return;
      }
      await self.clients.openWindow("/");
    })(),
  );
});
