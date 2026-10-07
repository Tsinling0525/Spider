import { defineConfig, loadEnv } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const headers: Record<string, string> = {};
  if (env.SPIDER_API_KEY) headers.Authorization = `Bearer ${env.SPIDER_API_KEY}`;
  return {
    plugins: [svelte()],
    server: {
      port: 5174,
      strictPort: true,
      proxy: {
        "/spider-api": {
          target: env.SPIDER_DASHBOARD_BASE_URL || "http://127.0.0.1:8083",
          changeOrigin: true,
          rewrite: (path: string) => path.replace(/^\/spider-api/, ""),
          headers,
          timeout: 240000,
          proxyTimeout: 240000,
        },
      },
    },
    build: { target: "safari16" },
  };
});
