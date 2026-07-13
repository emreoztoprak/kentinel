import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev-mode proxy: the Go backend runs on :8080 and owns /api and /healthz.
// ws:true lets the pod-exec WebSocket upgrade pass through.
export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    // fs.allow: the docs page imports ../README.md and ../../docs/*.md as raw
    // text; allow serving them from outside web/ in dev mode.
    fs: { allow: [".."] },
    proxy: {
      "/api": { target: "http://localhost:8080", ws: true },
      "/healthz": { target: "http://localhost:8080" },
    },
  },
});
