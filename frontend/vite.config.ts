import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const vitePort = Number(
  (globalThis as typeof globalThis & { process?: { env?: Record<string, string | undefined> } }).process?.env
    ?.WAILS_VITE_PORT,
) || 9245;

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        entryFileNames: "assets/app.js",
        chunkFileNames: "assets/[name]-[hash].js",
        assetFileNames: (assetInfo) => assetInfo.name?.slice(-4) === ".css" ? "assets/app.css" : "assets/[name]-[hash][extname]",
      },
    },
  },
  server: {
    port: vitePort,
    strictPort: true,
  },
});
