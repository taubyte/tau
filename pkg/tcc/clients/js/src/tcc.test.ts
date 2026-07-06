import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, statSync, readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { compileMap, decompileMap } from "./index.js";

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

test("compile produces the golden object and DNS validation", async () => {
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);
  assert.ok(files.size > 0, "fixture files loaded");

  const result = await compileMap(files, { branch: "master" });
  assert.ok(result.object, "has compiled object");
  assert.ok("functions" in result.object, "object has resources");

  const dns = result.validations.find((v) => v.validator === "dns");
  assert.equal(dns?.value, "hal.computers.com", "expected DNS validation present");
});

test("compile -> decompile -> recompile round-trips to the same object", async () => {
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);

  const result = await compileMap(files, { branch: "master" });
  const written = await decompileMap(result);
  assert.ok(written.has("/config.yaml"), "decompile wrote /config.yaml");
  assert.ok(written.size > 1, "decompile wrote resource files");

  const result2 = await compileMap(written, { branch: "master" });
  assert.deepEqual(result2.object, result.object, "recompiled object matches original");
});
