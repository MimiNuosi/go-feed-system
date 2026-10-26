import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";

const apiTarget = process.env.VITE_API_TARGET ?? "http://127.0.0.1:18080";

export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: true },
      "/livez": { target: apiTarget, changeOrigin: true },
      "/readyz": { target: apiTarget, changeOrigin: true },
    },
  },
  build: {
    outDir: "../internal/web/dist",
    emptyOutDir: true,
  },
});
