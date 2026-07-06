import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, statSync, readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { compile, decompile, FunctionConfig, type AsyncFs } from "./index.js";

// The golden fixture the Go compile/decompile tests use.
const FIXTURE = resolve(import.meta.dirname, "../../../taubyte/v1/fixtures/config");

function loadDir(dir: string, prefix: string, map: Map<string, Uint8Array>) {
  for (const name of readdirSync(dir)) {
    const abs = join(dir, name);
    const key = prefix + "/" + name;
    if (statSync(abs).isDirectory()) loadDir(abs, key, map);
    else map.set(key, new Uint8Array(readFileSync(abs)));
  }
}

// A minimal in-memory async filesystem (the shape lightning-fs exposes), so the
// tests exercise the real staging path (hydrate/flush) through the public API.
function memFs(files: Map<string, Uint8Array> = new Map()): AsyncFs & { files: Map<string, Uint8Array> } {
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
        const names = new Set<string>();
        for (const k of files.keys())
          if (k.startsWith(pre)) {
            const n = k.slice(pre.length).split("/")[0];
            if (n) names.add(n);
          }
        return [...names];
      },
      async stat(p) {
        const dir = !files.has(p) && isDir(p);
        return { isDirectory: () => dir };
      },
      async mkdir() {},
    },
  };
}

test("generated accessors map flat fields to nested config keys", () => {
  const fn = new FunctionConfig();
  fn.type = "https";
  fn.memory = 64_000_000;

  const data = fn.data as Record<string, any>;
  assert.equal(data.trigger.type, "https", "type -> trigger.type");
  assert.equal(data.execution.memory, 64_000_000, "memory -> execution.memory");
  assert.equal(fn.type, "https", "reads back through the accessor");
  assert.equal(fn.memory, 64_000_000);
});

test("compile produces the golden object and DNS validation", async () => {
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);
  assert.ok(files.size > 0, "fixture files loaded");

  const result = await compile(memFs(files), "/", { branch: "master" });
  assert.ok(result.object, "has compiled object");
  assert.ok("functions" in result.object, "object has resources");

  const dns = result.validations.find((v) => v.validator === "dns");
  assert.equal(dns?.value, "hal.computers.com", "expected DNS validation present");
});

test("compile -> decompile -> recompile round-trips to the same object", async () => {
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);

  const result = await compile(memFs(files), "/", { branch: "master" });

  const out = memFs();
  await decompile(out, "/", result);
  assert.ok(out.files.has("/config.yaml"), "decompile wrote /config.yaml");
  assert.ok(out.files.size > 1, "decompile wrote resource files");

  const result2 = await compile(out, "/", { branch: "master" });
  assert.deepEqual(result2.object, result.object, "recompiled object matches original");
});
