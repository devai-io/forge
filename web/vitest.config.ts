import react from "@vitejs/plugin-react";
import path from "path";
import { defineConfig } from "vitest/config";

// Kept separate from vite.config.ts so the production build's type-check never
// has to know about the test runner's options.
export default defineConfig({
  resolve: {
    alias: { "@": path.resolve(__dirname, "./src") },
  },
  plugins: [react()],
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: "./src/test/setup.ts",
    // index.css?raw returns the real file (accent.test.ts checks its tokens);
    // other CSS stays stubbed out.
    css: { include: [/index\.css/] },
  },
});
