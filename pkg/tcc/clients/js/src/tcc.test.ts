import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, statSync, readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { compile, open, decompile, kinds, type AsyncFs } from "./index.js";

// The golden fixture the Go compile/decompile tests use. TCC_FIXTURE lets the
// e2e harness point at it from a tmp package; otherwise resolve it in-tree.
const FIXTURE =
  process.env.TCC_FIXTURE ?? resolve(import.meta.dirname, "../../../taubyte/v1/fixtures/config");
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
      async unlink(p) {
        files.delete(p);
      },
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

test("session: list, application-scoped access, and delete", async () => {
  const session = await open(fixtureFs(), "/");

  assert.ok((await session.functionNames()).includes(FN_NAME), "list global functions");
  assert.ok((await session.applications()).includes("test_app1"), "list applications");
  assert.ok((await session.functionNames("test_app1")).includes("test_function2"), "list app functions");

  const appFn = session.function("test_function2", "test_app1");
  assert.equal(await appFn.memory(), "23MB", "application-scoped read");
  await appFn.setMemory("99MB");
  assert.equal(await appFn.memory(), "99MB", "application-scoped write");

  await session.function("test_function2_glob").delete();
  assert.ok(!(await session.functionNames()).includes("test_function2_glob"), "deleted function is gone");

  // save must persist the deletion (pruned), and the app-scoped edit
  const out = fixtureFs();
  await session.save(out, "/");
  assert.ok(!out.files.has("/functions/test_function2_glob.yaml"), "save pruned the deleted file");
  const reopened = await open(out, "/");
  assert.ok(!(await reopened.functionNames()).includes("test_function2_glob"), "deletion persisted across reopen");
  await session.close();
  await reopened.close();
});

test("session: an application is one address, mapped to its own document", async () => {
  const session = await open(fixtureFs(), "/");
  const app = session.application("test_app1");

  await app.setDescription("edited");
  const { path, yaml } = await app.serialize();
  assert.equal(path, "applications/test_app1/config.yaml", "the container's own document");
  assert.ok(yaml.includes("edited"));

  // the whole point: no sibling applications/test_app1.yaml is ever written
  const out = memFs();
  await session.save(out, "/");
  assert.ok(!out.files.has("/applications/test_app1.yaml"), "no sibling document");
  assert.ok(out.files.has("/applications/test_app1/config.yaml"));

  assert.deepEqual(await app.validate(), [], "an application validates as a container");
  await session.close();
});

test("session: whole-document diff, serialize, and path->address", async () => {
  const session = await open(fixtureFs(), "/");
  const fn = session.function(FN_NAME);

  const doc = await fn.doc();
  assert.equal(doc.id, FN_ID);

  // replace the document: nested edit + a removed key, comments untouched
  await fn.setDoc({ ...doc, description: "diffed", trigger: { type: "https" } });
  assert.equal(await fn.description(), "diffed");
  // NB: the wasm reports an absent field as null while the generated getters are
  // typed `| undefined` — pre-existing, so accept either rather than pin it.
  const method = await fn.method();
  assert.ok(method === undefined || method === null, "a key absent from the doc is deleted");

  const { path, yaml } = await fn.serialize();
  assert.equal(path, `functions/${FN_NAME}.yaml`);
  // The deletion must reach the FILE, not just the in-memory view. Asserting
  // through get() is what let a "deleted" key survive in the YAML: reads saw it
  // gone while save() wrote it straight back.
  assert.ok(!yaml.includes("method:"), `removed key still in the serialized YAML:\n${yaml}`);
  const saved = memFs();
  await session.save(saved, "/");
  const onDisk = new TextDecoder().decode(saved.files.get(`/functions/${FN_NAME}.yaml`)!);
  assert.ok(!onDisk.includes("method:"), `removed key still in the saved file:\n${onDisk}`);
  assert.ok(onDisk.includes("diffed"), "the edit itself must be saved");

  // ...and a diff that ONLY removes must reach the file too. This is the case
  // that isolates the bug: a deletion updated the in-memory tree but never
  // marked the document dirty, so it was written only as a side effect of some
  // OTHER edit to the same document. A removal on its own vanished silently.
  const cur = await fn.doc();
  delete (cur as Record<string, unknown>).description;
  await fn.setDoc(cur);
  const { yaml: afterRemoveOnly } = await fn.serialize();
  assert.ok(
    !afterRemoveOnly.includes("description:"),
    `a removal with no other change must reach the file:\n${afterRemoveOnly}`,
  );
  assert.deepEqual(await session.resourceAt(path), ["functions", FN_NAME], "path -> address");
  assert.equal(await session.resourceAt(".git/config.yaml"), null, "not a resource document");
  assert.deepEqual(
    await session.resourceAt("applications/test_app1/config.yaml"),
    ["applications", "test_app1"],
    "a container's document addresses the container",
  );
  await session.close();
});

test("session: generate mints a DSL-declared id, and validation is field-attributed", async () => {
  const session = await open(fixtureFs(), "/");
  const fn = session.function(FN_NAME);

  const id = await fn.generate(["id"]);
  assert.notEqual(id, FN_ID, "a fresh id, not the current one");
  await fn.validateField(["id"], id); // throws if it isn't a valid CID

  await fn.setType("nope" as never);
  const issues = await fn.validate();
  assert.equal(issues.length, 1);
  assert.deepEqual(issues[0]!.field, ["trigger", "type"], "the issue names the field");
  assert.ok(issues[0]!.message.includes("invalid value"));
  await session.close();
});

test("session: resourceRepo reads the backing repo without naming a provider", async () => {
  // A non-github provider block: if the extraction hardcoded "github" anywhere,
  // this returns null instead of naming gitlab.
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);
  files.set(
    "/libraries/gitlab_lib.yaml",
    new TextEncoder().encode(
      "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\n" +
        "name: gitlab_lib\n" +
        "source:\n  branch: main\n  gitlab:\n    id: '42'\n    fullname: acme/lib\n",
    ),
  );
  const session = await open(memFs(files), "/");
  assert.deepEqual(await session.resourceRepo(["libraries", "gitlab_lib"]), {
    provider: "gitlab",
    fullname: "acme/lib",
    branch: "main",
  });
  assert.equal(
    await session.resourceRepo(["functions", FN_NAME]),
    null,
    "a resource with no repo block is not repo-backed",
  );
  await session.close();
});

test("hydrate skips dot-entries, and a pruning save never deletes them", async () => {
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);
  const sentinel = "/.git/objects/ab/cdef";
  files.set(sentinel, new TextEncoder().encode("git object"));
  const fs = memFs(files);

  const session = await open(fs, "/");
  await session.function(FN_NAME).setMemory("64GB");
  await session.save(fs, "/"); // prunes deleted files
  assert.ok(fs.files.has(sentinel), "a pruning save must not delete .git");
  await session.close();
});

// The generic path: a caller that knows a kind only as a string never builds an
// address, pluralizes a name, or special-cases the container kind.
test("session: kind-keyed addressing, listing and existence", async () => {
  const session = await open(fixtureFs(), "/");

  const kinds = await session.kinds();
  const fnKind = kinds.find((k) => k.group === "functions")!;
  assert.equal(fnKind.name, "function", "a kind's declared singular");
  assert.equal(fnKind.container, false);
  // An application is a CONTAINER, not a resource: the DSL declares no
  // Resource() for it, so it has no compiled resource type. Editing it is
  // uniform (address/doc/serialize/validate below); "which kinds can I create as
  // resources" is the container flag, not a name check.
  assert.equal(kinds.find((k) => k.group === "applications")?.container, true);
  assert.deepEqual(
    kinds.filter((k) => !k.container).map((k) => k.group).sort(),
    ["databases", "domains", "functions", "libraries", "messaging", "services", "smartops", "storages", "websites"],
    "the resource kinds are exactly the nine the DSL declares Resource() for",
  );
  // and a group the DSL never names (clouds — a leaf map in the root document)
  // is not reported as a kind at all
  assert.equal(kinds.find((k) => k.group === "clouds"), undefined);
  assert.ok(kinds.every((k) => k.name !== ""));

  // both the group key and the singular resolve to the same address
  assert.deepEqual(await session.address("functions", FN_NAME), ["functions", FN_NAME]);
  assert.deepEqual(await session.address("function", FN_NAME), ["functions", FN_NAME]);
  assert.deepEqual(await session.address("function", "f", "web"), [
    "applications",
    "web",
    "functions",
    "f",
  ]);
  assert.deepEqual(await session.address("application", "web"), ["applications", "web"]);
  await assert.rejects(() => session.address("functionz", "x"), /unknown kind/);
  // containers don't nest, and saying so beats silently building a bad address
  await assert.rejects(() => session.address("application", "web", "other"), /container kind/);

  // listing by kind, project scope and application scope
  assert.ok((await session.names("function")).includes(FN_NAME));
  assert.ok((await session.names("function", "test_app1")).includes("test_function2"));
  assert.ok((await session.names("application")).includes("test_app1"));
  assert.deepEqual(await session.names("function", "no_such_app"), []);

  // existence — what tells an editor a resource is new
  assert.equal(await session.exists(["functions", FN_NAME]), true);
  assert.equal(await session.exists(["functions", "never_created"]), false);
  assert.equal(await session.exists(["applications", "test_app1"]), true);

  // the untyped accessor has the same document surface as the typed one
  const fn = await session.resource("function", FN_NAME);
  assert.deepEqual(fn.res, ["functions", FN_NAME]);
  assert.equal((await fn.doc()).id, FN_ID);
  assert.equal((await fn.serialize()).path, `functions/${FN_NAME}.yaml`);
  assert.equal(await fn.exists(), true);

  const fresh = await session.resource("function", "brand_new");
  assert.equal(await fresh.exists(), false);
  await fresh.setDoc({ id: await fresh.generate(["id"]), trigger: { type: "https" } });
  assert.equal(await fresh.exists(), true, "created through the generic path");

  // and it works for the container kind with no special case at the call site
  const app = await session.resource("application", "test_app1");
  assert.equal((await app.serialize()).path, "applications/test_app1/config.yaml");
  await session.close();
});

// A field whose location BRANCHES (a storage's size lives under object/ or
// streaming/) and the key that selects the branch used to have no accessor at
// all — the generator skipped both — so the console could only reach a required
// field through setDoc.
test("session: branching fields and their discriminator are addressable", async () => {
  const session = await open(fixtureFs(), "/");

  const streaming = session.storage("test_storage1");
  assert.equal(await streaming.type(), "streaming", "the key that selects the branch");
  assert.equal(await streaming.size("streaming"), "30GB");
  const otherBranch = await streaming.size("object"); // absent reads as null, see note above
  assert.ok(otherBranch === undefined || otherBranch === null, "the other branch is empty");

  const object = session.storage("test_storage2", "test_app1");
  assert.equal(await object.type(), "object");
  assert.equal(await object.size("object"), "50GB");

  // writes land in the named branch and reach the file
  await streaming.setSize("streaming", "64GB");
  const { yaml } = await streaming.serialize();
  assert.ok(/streaming:[\s\S]*size: 64GB/.test(yaml), `size not written under streaming:\n${yaml}`);

  // a nested discriminator reads from the block that holds it, not the root
  assert.equal(await session.library("test_library1").provider(), "github");
  await session.close();
});

// Two things a caller should not need a project for, or a whole document for.
test("kinds needs no session, and location needs no document read", async () => {
  // the vocabulary is a property of the schema — no project, no session
  const ks = await kinds();
  assert.ok(ks.length > 0);
  assert.deepEqual(
    ks.find((k) => k.group === "functions"),
    { name: "function", group: "functions", container: false },
  );
  assert.equal(ks.find((k) => k.group === "applications")?.container, true);

  const session = await open(fixtureFs(), "/");
  const fn = session.function(FN_NAME);

  // location agrees with serialize's path, without rendering the document
  const { path } = await fn.serialize();
  assert.equal(await fn.location(), path);
  assert.equal(await session.application("test_app1").location(), "applications/test_app1/config.yaml");

  // and it still refuses to invent a path for a resource that has no document
  await assert.rejects(() => session.function("no_such_function").location());
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
