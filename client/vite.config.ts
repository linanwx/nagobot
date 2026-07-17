import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Dev-time backend: a locally running nagobot daemon web channel.
// Override with NAGOBOT_API=http://host:port when the daemon runs elsewhere.
const backend = process.env.NAGOBOT_API ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    // Listen on all interfaces so phones/tablets on the LAN can open the dev
    // client at http://<mac-ip>:5173 (API/WS are proxied server-side below).
    host: true,
    proxy: {
      "/api": { target: backend, changeOrigin: true },
      // rewriteWsOrigin: the daemon's websocket library enforces same-origin,
      // so the proxied handshake must carry the backend's origin.
      "/ws": { target: backend, changeOrigin: true, ws: true, rewriteWsOrigin: true },
    },
  },
});
