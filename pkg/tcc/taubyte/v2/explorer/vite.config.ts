import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The fixture lives one level up, in pkg/tcc/taubyte/v2/fixtures — the same tree the
// Go tests compile. Allowing it here is what keeps one project as the single example
// rather than a copy that drifts from what the tests assert.
export default defineConfig({
  plugins: [react()],
  server: { fs: { allow: [".."] }, port: 5177 },
});
