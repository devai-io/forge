import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import path from "path";
import { defineConfig } from "vite";

export default defineConfig({
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  plugins: [react(), tailwindcss()],
  build: {
    rollupOptions: {
      output: {
        // Vendor code changes far less often than the app; separate chunks keep
        // it cached across deploys (hashed filenames, immutable in nginx).
        manualChunks: {
          react: ["react", "react-dom", "react-router-dom"],
          query: ["@tanstack/react-query"],
          markdown: ["react-markdown", "remark-gfm"],
          icons: ["lucide-react"],
          qrcode: ["qrcode"],
        },
      },
    },
  },
  server: {
    host: "0.0.0.0",
    port: 5175,
    // The SPA always calls same-origin /api/*: in production Caddy routes that
    // prefix to forge-api, and in development this proxy stands in for it.
    // FORGE_API_URL overrides the target when the API runs somewhere else.
    proxy: {
      "/api": {
        target: process.env.FORGE_API_URL ?? "http://127.0.0.1:8080",
        changeOrigin: true,
        // The terminal attaches over a WebSocket under /api.
        ws: true,
      },
      // VS Code (web), proxied by forge-api: HTML, assets and its WebSockets.
      "/code": {
        target: process.env.FORGE_API_URL ?? "http://127.0.0.1:8080",
        changeOrigin: true,
        ws: true,
      },
    },
  },
});
