import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, statSync, readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { compile, open, decompile, type AsyncFs } from "./index.js";

// The golden fixture the Go compile/decompile tests use.
const FIXTURE = resolve(import.meta.dirname, "../../../taubyte/v1/fixtures/config");
const FN_NAME = "test_function1_glob";
const FN_ID = "QmNf1SAZuyM9vLPeWiYx9qh3AWJKCjJvF9d1f5ZPZCZxXh";

function loadDir(dir: string, prefix: string, map: Map<string, Uint8Array>) {
  for (const name of readdirSync(dir)) {
    const abs = join(dir, name);
    const key = prefix + "/" + name;
    if (statSync(abs).isDirectory()) loadDir(abs, key, map);
    else map.set(key, new Uint8Array(readFileSync(abs)));
  }
}

// A minimal in-memory async filesystem (the shape lightning-fs exposes).
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

function fixtureFs() {
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);
  return memFs(files);
}

test("compile produces the golden object and DNS validation", async () => {
  const result = await compile(fixtureFs(), "/", { branch: "master" });
  assert.ok("functions" in result.object, "object has resources");
  const dns = result.validations.find((v) => v.validator === "dns");
  assert.equal(dns?.value, "hal.computers.com", "expected DNS validation present");
});

test("session: edit typed fields, compile reflects the edit, save writes YAML", async () => {
  const session = await open(fixtureFs(), "/");
  const fn = session.function(FN_NAME);

  assert.equal(await fn.memory(), "32GB", "read source memory");
  assert.equal(await fn.type(), "http", "read typed enum");

  await fn.setMemory("64GB");
  await fn.setType("https");
  assert.equal(await fn.memory(), "64GB", "read back edited memory");
  assert.equal(await fn.type(), "https");

  const compiled = await session.compile({ branch: "master" });
  assert.equal(
    (compiled.object.functions as any)[FN_ID].memory,
    64000000000,
    "compiled memory reflects 64GB edit",
  );

  const out = memFs();
  await session.save(out, "/");
  const yaml = new TextDecoder().decode(out.files.get(`/functions/${FN_NAME}.yaml`)!);
  assert.ok(yaml.includes("memory: 64GB"), "saved YAML has the edit");
  assert.ok(yaml.includes("id: " + FN_ID), "saved YAML preserved id");
  await session.close();
});

test("decompile a compiled object into an editable session", async () => {
  const compiled = await compile(fixtureFs(), "/", { branch: "master" });
  const session = await decompile(compiled);
  const fn = session.function(FN_NAME);
  // decompiled source uses the human form again
  assert.equal(await fn.memory(), "32GB", "decompiled memory is the source form");
  await fn.setMemory("128GB");
  const recompiled = await session.compile({ branch: "master" });
  assert.equal((recompiled.object.functions as any)[FN_ID].memory, 128000000000);
  await session.close();
});
