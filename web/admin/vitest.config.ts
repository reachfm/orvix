import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Vitest runs only unit/component tests. Playwright end-to-end specs under
// tests/e2e are executed by `npm run test:e2e` and must not be collected here
// (their `@playwright/test` fixtures are incompatible with the vitest runner).
export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    exclude: ["tests/e2e/**", "node_modules/**", "dist/**"],
  },
});
