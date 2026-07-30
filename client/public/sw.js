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

// Clicking a notification lands on the session it came from. Three cases, in
// order: a window already showing that session is simply focused; any other
// open window is focused and told to switch (a service worker cannot navigate
// React state, so App.tsx listens for this message); with nothing open, a new
// window opens straight at the session's address.
//
// Session keys contain a colon ("web:2118acc7"), so the address always carries
// it encoded — and the comparison goes through searchParams, which DECODES.
// Comparing raw URL text instead would never match a window we ourselves
// opened, and every click would pile up another tab.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const session = (event.notification.data && event.notification.data.session) || "";
  event.waitUntil(
    (async () => {
      const all = await self.clients.matchAll({
        type: "window",
        includeUncontrolled: true,
      });
      if (session) {
        for (const client of all) {
          if (new URL(client.url).searchParams.get("s") === session) {
            await client.focus();
            return;
          }
        }
      }
      if (all.length > 0) {
        await all[0].focus();
        if (session) all[0].postMessage({ type: "open-session", session });
        return;
      }
      await self.clients.openWindow(
        session ? `/?s=${encodeURIComponent(session)}` : "/",
      );
    })(),
  );
});
