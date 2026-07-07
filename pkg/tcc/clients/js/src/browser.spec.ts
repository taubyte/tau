import { expect } from "@esm-bundle/chai";
import { compile, open, type AsyncFs, type WasmAssets } from "./index.js";

// A minimal valid project bundled inline (the browser has no filesystem): a
// function referencing a domain, so compile also emits the DNS validation.
const FILES: Record<string, string> = {
  "/config.yaml": ["id: QmTz6X9hTn18fpKxrnbE3BvmkZHy3r1mRyHzfXK3gVZLxR", "name: TrueTest", "notification:", "  email: cto@taubyte.com", ""].join(
    "\n",
  ),
  "/domains/hal.yaml": [
    "id: QmUcVJtgGZYkqFr2J9t2jV2fJJWZBvD7FJ6RyXzJY2kAj1",
    "fqdn: hal.computers.com",
    "certificate:",
    "  type: inline",
    "  key: k",
    "  cert: c",
    "",
  ].join("\n"),
  "/functions/hello.yaml": [
    "id: QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh",
    "trigger:",
    "  type: https",
    "  method: GET",
    "  paths:",
    "    - /hello",
    "domains:",
    "  - hal",
    "source: .",
    "execution:",
    "  timeout: 20s",
    "  memory: 32MB",
    "  call: hello",
    "",
  ].join("\n"),
};

function memFs(): AsyncFs & { files: Map<string, Uint8Array> } {
  const files = new Map<string, Uint8Array>();
  const isDir = (p: string) => {
    const pre = p.endsWith("/") ? p : p + "/";
    for (const k of files.keys()) if (k.startsWith(pre)) return true;
    return p === "/";
  };
  return {
    files,
    promises: {
      async readFile(p) {
        const v = files.get(p);
        if (!v) throw new Error("ENOENT: " + p);
        return v;
      },
      async writeFile(p, d) {
        files.set(p, d);
      },
      async readdir(p) {
        const pre = p === "/" ? "/" : p + "/";
        const s = new Set<string>();
        for (const k of files.keys())
          if (k.startsWith(pre)) {
            const n = k.slice(pre.length).split("/")[0];
            if (n) s.add(n);
          }
        return [...s];
      },
      async stat(p) {
        const dir = !files.has(p) && isDir(p);
        return { isDirectory: () => dir };
      },
      async mkdir() {},
      async unlink(p) {
        files.delete(p);
      },
    },
  };
}

async function seeded() {
  const fs = memFs();
  const enc = new TextEncoder();
  for (const [p, c] of Object.entries(FILES)) await fs.promises.writeFile(p, enc.encode(c));
  return fs;
}

async function assets(): Promise<WasmAssets> {
  const [js, wasm] = await Promise.all([fetch("/assets/wasm_exec.js"), fetch("/assets/tcc.wasm")]);
  return { wasmExecSource: await js.text(), wasmBytes: await wasm.arrayBuffer() };
}

describe("tcc in a real browser", () => {
  it("instantiates the wasm and compiles a project", async () => {
    const res = await compile(await seeded(), "/", { branch: "main" }, await assets());
    expect(res.object).to.have.property("functions");
    const dns = res.validations.find((v) => v.validator === "dns");
    expect(dns?.value).to.equal("hal.computers.com");
  });

  it("edits typed fields through a session", async () => {
    const session = await open(await seeded(), "/", await assets());
    const fn = session.function("hello");
    expect(await fn.memory()).to.equal("32MB");
    await fn.setMemory("64MB");
    expect(await fn.memory()).to.equal("64MB");
    expect(await fn.type()).to.equal("https");
    await session.close();
  });
});
