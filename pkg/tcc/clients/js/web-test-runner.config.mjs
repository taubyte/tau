import { esbuildPlugin } from "@web/dev-server-esbuild";
import { chromeLauncher } from "@web/test-runner-chrome";

// Runs the *.spec.ts tests in a real (headless) browser. The wasm assets under
// ./assets are served by the dev server, so the tests fetch and instantiate the
// module exactly as a browser app would. Uses the system Chrome (no download).
export default {
  files: "src/*.spec.ts",
  nodeResolve: true,
  plugins: [esbuildPlugin({ ts: true, target: "es2022" })],
  browsers: [
    chromeLauncher({
      launchOptions: {
        executablePath: process.env.CHROME_BIN || "/usr/bin/google-chrome-stable",
        headless: true,
        args: ["--no-sandbox"],
      },
    }),
  ],
  testFramework: { config: { timeout: "20000" } },
};
