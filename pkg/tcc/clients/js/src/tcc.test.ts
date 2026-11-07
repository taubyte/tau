import { test } from "node:test";
import assert from "node:assert/strict";
import { readdirSync, statSync, readFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { compile, open, decompile, kinds, type AsyncFs } from "./index.js";

// The golden fixture the Go compile/decompile tests use. TCC_FIXTURE lets the
// e2e harness point at it from a tmp package; otherwise resolve it in-tree.
const FIXTURE =
  process.env.TCC_FIXTURE ?? resolve(import.meta.dirname, "../../../taubyte/v2/fixtures/basic");
const FN_NAME = "redact";
const FN_ID = "QmPZ9XwnqQzhLTPnbTZWpqAqLmv5qJpsKHYS8FfKCYPYuK";

// v2's vocabulary is singular all the way down — a kind's group key IS its name,
// and the one container kind is `namespace`. v1 said `functions`/`applications`
// and this file used to say so too; nothing here should reintroduce a plural.
const CONTAINER = "namespace";
const NS = "shop";

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

function fixtureFiles() {
  const files = new Map<string, Uint8Array>();
  loadDir(FIXTURE, "", files);
  return files;
}

function fixtureFs() {
  return memFs(fixtureFiles());
}

function write(files: Map<string, Uint8Array>, path: string, body: string) {
  files.set(path, new TextEncoder().encode(body));
}

test("compile produces the golden object and DNS validation", async () => {
  const result = await compile(fixtureFs(), "/", { branch: "main" });
  assert.ok("function" in result.object, "object has resources");
  const dns = result.validations.find((v) => v.validator === "dns");
  assert.equal(dns?.value, "example.com", "expected DNS validation present");
});

test("session: edit typed fields, compile reflects the edit, save writes YAML", async () => {
  const session = await open(fixtureFs(), "/");
  const fn = session.function(FN_NAME);

  assert.equal(await fn.memory(), "16MB", "read source memory");
  // v1 read `trigger.type`; v2 functions carry no trigger at all, by design
  // (TestFunctionHasNoTrigger keeps it that way). `implements` is v2's closed
  // set on the same resource — the same claim about the same machinery.
  assert.equal(await fn.implements(), "gateway-transformer", "read typed enum");

  await fn.setMemory("64MB");
  await fn.setImplements("proxy-transformer");
  assert.equal(await fn.memory(), "64MB", "read back edited memory");
  assert.equal(await fn.implements(), "proxy-transformer");

  const compiled = await session.compile({ branch: "main" });
  // v1 compiled memory to the resource root; v2 keeps it under `execution`,
  // where the DSL declares it.
  assert.equal(
    (compiled.object.function as any)[FN_ID].execution.memory,
    64000000,
    "compiled memory reflects 64MB edit",
  );

  const out = memFs();
  await session.save(out, "/");
  const yaml = new TextDecoder().decode(out.files.get(`/function/${FN_NAME}.yaml`)!);
  assert.ok(yaml.includes("memory: 64MB"), "saved YAML has the edit");
  assert.ok(yaml.includes("id: " + FN_ID), "saved YAML preserved id");
  await session.close();
});

test("session: list, namespace-scoped access, and delete", async () => {
  const session = await open(fixtureFs(), "/");

  assert.ok((await session.functionNames()).includes(FN_NAME), "list global functions");
  assert.ok((await session.namespaceNames()).includes(NS), "list namespaces");
  assert.ok((await session.functionNames(NS)).includes("notify"), "list namespaced functions");

  const nsFn = session.function("notify", NS);
  assert.equal(await nsFn.memory(), "16MB", "namespace-scoped read");
  await nsFn.setMemory("99MB");
  assert.equal(await nsFn.memory(), "99MB", "namespace-scoped write");

  await session.function("poll_mentions").delete();
  assert.ok(!(await session.functionNames()).includes("poll_mentions"), "deleted function is gone");

  // save must persist the deletion (pruned), and the namespace-scoped edit
  const out = fixtureFs();
  await session.save(out, "/");
  assert.ok(!out.files.has("/function/poll_mentions.yaml"), "save pruned the deleted file");
  const reopened = await open(out, "/");
  assert.ok(!(await reopened.functionNames()).includes("poll_mentions"), "deletion persisted across reopen");
  await session.close();
  await reopened.close();
});

test("session: a namespace is one address, mapped to its own document", async () => {
  const session = await open(fixtureFs(), "/");
  const ns = session.namespace(NS);

  await ns.setDescription("edited");
  const { path, yaml } = await ns.serialize();
  assert.equal(path, `${CONTAINER}/${NS}/meta.yaml`, "the container's own document");
  assert.ok(yaml.includes("edited"));

  // the whole point: no sibling namespace/shop.yaml is ever written
  const out = memFs();
  await session.save(out, "/");
  assert.ok(!out.files.has(`/${CONTAINER}/${NS}.yaml`), "no sibling document");
  assert.ok(out.files.has(`/${CONTAINER}/${NS}/meta.yaml`));

  assert.deepEqual(await ns.validate(), [], "a namespace validates as a container");
  await session.close();
});

test("session: whole-document diff, serialize, and path->address", async () => {
  const session = await open(fixtureFs(), "/");
  const fn = session.function(FN_NAME);

  const doc = await fn.doc();
  assert.equal(doc.id, FN_ID);

  // replace the document: nested edit + a removed key, comments untouched
  const { implements: _dropped, ...withoutImplements } = doc as Record<string, unknown>;
  await fn.setDoc({ ...withoutImplements, description: "diffed" });
  assert.equal(await fn.description(), "diffed");
  // NB: the wasm reports an absent field as null while the generated getters are
  // typed `| undefined` — pre-existing, so accept either rather than pin it.
  const implemented = await fn.implements();
  assert.ok(implemented === undefined || implemented === null, "a key absent from the doc is deleted");

  const { path, yaml } = await fn.serialize();
  assert.equal(path, `function/${FN_NAME}.yaml`);
  // The deletion must reach the FILE, not just the in-memory view. Asserting
  // through get() is what let a "deleted" key survive in the YAML: reads saw it
  // gone while save() wrote it straight back.
  assert.ok(!yaml.includes("implements:"), `removed key still in the serialized YAML:\n${yaml}`);
  const saved = memFs();
  await session.save(saved, "/");
  const onDisk = new TextDecoder().decode(saved.files.get(`/function/${FN_NAME}.yaml`)!);
  assert.ok(!onDisk.includes("implements:"), `removed key still in the saved file:\n${onDisk}`);
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
  assert.deepEqual(await session.resourceAt(path), ["function", FN_NAME], "path -> address");
  assert.equal(await session.resourceAt(".git/config.yaml"), null, "not a resource document");
  assert.deepEqual(
    await session.resourceAt(`${CONTAINER}/${NS}/meta.yaml`),
    [CONTAINER, NS],
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

  await fn.setImplements("nope" as never);
  const issues = await fn.validate();
  assert.equal(issues.length, 1);
  assert.deepEqual(issues[0]!.field, ["implements"], "the issue names the field");
  assert.ok(issues[0]!.message.includes("invalid value"));
  await session.close();
});

// v1 asserted this through a generated `resourceRepo`, which read whichever
// sub-object of `source` carried a repo name so that no provider was ever
// hardcoded. v2 generates no such method and cannot: the DSL replaced the
// provider block (`github: {fullname}`) with one `source.git` clone URL, so
// there is no provider key left to hardcode OR to read. The claim survives as
// the stronger one — the same accessors read any host — and is asserted here
// against the shape that replaced it.
test("session: a git backing is a URL, so no provider is named anywhere", async () => {
  const files = fixtureFiles();
  // A component on a self-hosted GitLab: if anything in the walk special-cased
  // github, these read as undefined instead of naming the host.
  write(
    files,
    "/component/gitlab_lib.yaml",
    "id: QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o\n" +
      "description: a component that lives nowhere near github\n" +
      "source:\n  git: https://gitlab.acme.internal/acme/lib\n  branch: release\n" +
      "build:\n  script:\n    image: taubyte/go-wasi:v2\n    path: .taubyte/build.sh\n    output: lib.wasm\n",
  );
  const session = await open(memFs(files), "/");

  const elsewhere = session.component("gitlab_lib");
  assert.equal(await elsewhere.kind(), "git", "the source discriminator, whoever hosts it");
  assert.equal(await elsewhere.git(), "https://gitlab.acme.internal/acme/lib");
  assert.equal(await elsewhere.branch(), "release");

  // ...and the fixture's own, on a different host, through the same accessors.
  const paylib = session.component("paylib");
  assert.equal(await paylib.git(), "https://github.com/acme/paylib");
  assert.equal(await paylib.branch(), "main");

  // A resource with no git block is not repo-backed, and says so rather than
  // inventing a host. (Absent reads as null, see the note above.)
  const unbacked = await session.function(FN_NAME).git();
  assert.ok(unbacked === undefined || unbacked === null, "no git block is not repo-backed");
  await session.close();
});

test("hydrate skips dot-entries, and a pruning save never deletes them", async () => {
  const files = fixtureFiles();
  const sentinel = "/.git/objects/ab/cdef";
  write(files, sentinel, "git object");
  const fs = memFs(files);

  const session = await open(fs, "/");
  await session.function(FN_NAME).setMemory("64MB");
  await session.save(fs, "/"); // prunes deleted files
  assert.ok(fs.files.has(sentinel), "a pruning save must not delete .git");
  await session.close();
});

// The generic path: a caller that knows a kind only as a string never builds an
// address, pluralizes a name, or special-cases the container kind.
test("session: kind-keyed addressing, listing and existence", async () => {
  const session = await open(fixtureFs(), "/");

  const ks = await session.kinds();
  const fnKind = ks.find((k) => k.group === "function")!;
  assert.equal(fnKind.name, "function", "a kind's declared singular");
  assert.equal(fnKind.container, false);
  // A namespace is a CONTAINER, not a resource: the DSL declares no Resource()
  // for it, so it has no compiled resource type. Editing it is uniform
  // (address/doc/serialize/validate below); "which kinds can I create as
  // resources" is the container flag, not a name check.
  assert.equal(ks.find((k) => k.group === CONTAINER)?.container, true);
  assert.deepEqual(
    ks.filter((k) => !k.container).map((k) => k.group).sort(),
    [
      "agent",
      "service",
      "bus",
      "cache",
      "channel",
      "component",
      "cron",
      "database",
      "function",
      "gateway",
      "mcp",
      "model",
      "proxy",
      "sandbox",
      "secret",
      "storage",
      "stream",
      "sync",
      "volume",
      "website",
      "worker",
      "workflow",
      "zone",
    ],
    "the resource kinds are exactly the ones the DSL declares Resource() for",
  );
  // and a group the DSL never names (regions — a leaf list in the root
  // document) is not reported as a kind at all
  assert.equal(ks.find((k) => k.group === "regions"), undefined);
  assert.ok(ks.every((k) => k.name !== ""));

  // in v2 the group key IS the singular, and both still resolve
  assert.deepEqual(await session.address("function", FN_NAME), ["function", FN_NAME]);
  assert.deepEqual(await session.address("function", "f", "web"), [
    CONTAINER,
    "web",
    "function",
    "f",
  ]);
  assert.deepEqual(await session.address(CONTAINER, "web"), [CONTAINER, "web"]);
  await assert.rejects(() => session.address("functionz", "x"), /unknown kind/);
  // containers don't nest, and saying so beats silently building a bad address
  await assert.rejects(() => session.address(CONTAINER, "web", "other"), /container kind/);

  // listing by kind, project scope and namespace scope
  assert.ok((await session.names("function")).includes(FN_NAME));
  assert.ok((await session.names("function", NS)).includes("notify"));
  assert.ok((await session.names(CONTAINER)).includes(NS));
  assert.deepEqual(await session.names("function", "no_such_namespace"), []);

  // existence — what tells an editor a resource is new
  assert.equal(await session.exists(["function", FN_NAME]), true);
  assert.equal(await session.exists(["function", "never_created"]), false);
  assert.equal(await session.exists([CONTAINER, NS]), true);

  // the untyped accessor has the same document surface as the typed one
  const fn = await session.resource("function", FN_NAME);
  assert.deepEqual(fn.res, ["function", FN_NAME]);
  assert.equal((await fn.doc()).id, FN_ID);
  assert.equal((await fn.serialize()).path, `function/${FN_NAME}.yaml`);
  assert.equal(await fn.exists(), true);

  const fresh = await session.resource("function", "brand_new");
  assert.equal(await fresh.exists(), false);
  await fresh.setDoc({ id: await fresh.generate(["id"]), implements: "handler" });
  assert.equal(await fresh.exists(), true, "created through the generic path");

  // and it works for the container kind with no special case at the call site
  const ns = await session.resource(CONTAINER, NS);
  assert.equal((await ns.serialize()).path, `${CONTAINER}/${NS}/meta.yaml`);
  await session.close();
});

// A field whose location BRANCHES (a storage's size lives under object/ or
// streaming/) and the key that selects the branch used to have no accessor at
// all — the generator skipped both — so the console could only reach a required
// field through setDoc.
test("session: branching fields and their discriminator are addressable", async () => {
  const files = fixtureFiles();
  // The fixture's only storage is an object one; a streaming storage is written
  // here rather than into the shared fixture so this file's needs do not move
  // what every Go test compiles.
  write(
    files,
    `/${CONTAINER}/${NS}/storage/feed.yaml`,
    "id: QmZULkCELmmk5XNfCgTnCyFgAVxBRBXyDHGGMVoLFLiXEN\n" +
      "description: the other branch of the same field\n" +
      "streaming:\n    size: 30GB\n    ttl: 1h\n",
  );
  const session = await open(memFs(files), "/");

  const streaming = session.storage("feed", NS);
  assert.equal(await streaming.type(), "streaming", "the key that selects the branch");
  assert.equal(await streaming.size("streaming"), "30GB");
  const otherBranch = await streaming.size("object"); // absent reads as null, see note above
  assert.ok(otherBranch === undefined || otherBranch === null, "the other branch is empty");

  const object = session.storage("receipts");
  assert.equal(await object.type(), "object");
  assert.equal(await object.size("object"), "1GB");

  // writes land in the named branch and reach the file
  await streaming.setSize("streaming", "64GB");
  const { yaml } = await streaming.serialize();
  assert.ok(/streaming:[\s\S]*size: 64GB/.test(yaml), `size not written under streaming:\n${yaml}`);

  // a nested discriminator reads from the block that holds it, not the root
  assert.equal(await session.component("paylib").buildKind(), "script");
  await session.close();
});

// Two things a caller should not need a project for, or a whole document for.
test("kinds needs no session, and location needs no document read", async () => {
  // the vocabulary is a property of the schema — no project, no session
  const ks = await kinds();
  assert.ok(ks.length > 0);
  // Every field the Kind contract declares, exactly. v1 compared the whole
  // object; the standalone kinds() carries the explorer's own metadata beside
  // these three (category, doc, icon, fields), so a whole-object comparison
  // would assert the absence of things v2 deliberately added.
  const fnKind = ks.find((k) => k.group === "function")!;
  assert.equal(fnKind.name, "function");
  assert.equal(fnKind.group, "function");
  assert.equal(fnKind.container, false);
  assert.equal(ks.find((k) => k.group === CONTAINER)?.container, true);

  const session = await open(fixtureFs(), "/");
  const fn = session.function(FN_NAME);

  // location agrees with serialize's path, without rendering the document
  const { path } = await fn.serialize();
  assert.equal(await fn.location(), path);
  assert.equal(await session.namespace(NS).location(), `${CONTAINER}/${NS}/meta.yaml`);

  // and it still refuses to invent a path for a resource that has no document
  await assert.rejects(() => session.function("no_such_function").location());
  await session.close();
});

test("decompile a compiled object into an editable session", async () => {
  const compiled = await compile(fixtureFs(), "/", { branch: "main" });
  const session = await decompile(compiled);
  const fn = session.function(FN_NAME);
  // decompiled source uses the human form again
  assert.equal(await fn.memory(), "16MB", "decompiled memory is the source form");
  await fn.setMemory("128MB");
  const recompiled = await session.compile({ branch: "main" });
  assert.equal((recompiled.object.function as any)[FN_ID].execution.memory, 128000000);
  await session.close();
});

test("views: the build with no regions refuses a project naming regions; a preview cuts by region", async () => {
  // The fixture names regions, so the strict build refuses it and says why…
  const strict = await compile(fixtureFs(), "/", { branch: "main" });
  assert.ok(strict.views?.refused, "a project naming regions is refused by the build with none");
  assert.equal(strict.views?.views, undefined);

  // …a preview of region-1 alone holds everything there…
  const r1 = await compile(fixtureFs(), "/", { branch: "main", regions: ["region-1"] });
  assert.deepEqual(r1.views?.destinations, ["region-1"]);
  const one = r1.views!.views!["region-1"];
  assert.equal(one.empty, false);
  assert.deepEqual(Object.keys((one.object as any).object.function).sort(), Object.keys((r1.object as any).function).sort());

  // …and a session asks for the same cut, with the canonical object beside it.
  const session = await open(fixtureFs(), "/");
  const viewed = await session.views({ branch: "main", regions: ["region-1"] });
  assert.deepEqual(Object.keys(viewed.views!), ["region-1"]);
  assert.ok(viewed.canonical, "the whole object travels with the views");

  // A preview of the regions it names cuts one view for each.
  const named = await session.views({ branch: "main", regions: "named" });
  assert.deepEqual(named.destinations, ["region-1", "region-2"]);
  assert.equal(named.refused, undefined);
  assert.deepEqual(Object.keys(named.views!).sort(), ["region-1", "region-2"]);
});
