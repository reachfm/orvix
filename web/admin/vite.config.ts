/// <reference types="vitest" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  base: "/admin",
  build: {
    outDir: "dist",
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: {
          vendor: ["react", "react-dom", "recharts"],
        },
      },
    },
  },
  server: {
    port: 3001,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
  test: {
    pool: "threads",
    exclude: ["tests/e2e/**", "node_modules/**", "dist/**"],
  },
});
