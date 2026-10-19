import * as fs from "fs/promises";
import * as os from "os";
import * as path from "path";

import { Document } from "./Document";

describe("Document", () => {
  let dir: string;
  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "doc-"));
  });
  afterEach(async () => {
    await fs.rm(dir, { recursive: true, force: true });
  });

  it("round-trips a directory", async () => {
    await fs.mkdir(path.join(dir, "keys"));
    await fs.writeFile(path.join(dir, "cloud.yaml"), "domain:\n  root: a.com\n");
    await fs.writeFile(path.join(dir, "keys", "swarm.key"), Buffer.from([0, 1, 2, 255]));

    const doc = await Document.fromDir(dir);
    expect([...doc.files.keys()].sort()).toEqual(["cloud.yaml", "keys/swarm.key"]);
    expect(doc.text("cloud.yaml")).toBe("domain:\n  root: a.com\n");
    expect(Array.from(doc.get("keys/swarm.key")!)).toEqual([0, 1, 2, 255]);

    const out = path.join(dir, "out");
    await doc.toDir(out);
    const back = await Document.fromDir(out);
    expect(back.files).toEqual(doc.files);
  });

  it("builds from an object and a message", () => {
    const doc = Document.fromObject({ "a.yaml": "x: 1\n", "b.key": new Uint8Array([7]) });
    expect(doc.text("a.yaml")).toBe("x: 1\n");
    const again = Document.fromMessage(doc.toMessage());
    expect(again.files).toEqual(doc.files);
    expect(Document.fromMessage(undefined).files.size).toBe(0);
    expect(new Document(doc.files).files).toEqual(doc.files);
  });

  it("normalizes, gets, and deletes", () => {
    const doc = new Document().set("keys/../cloud.yaml", "a").set("keys\\x.key", "b");
    expect([...doc.files.keys()]).toEqual(["cloud.yaml", "keys/x.key"]);
    expect(doc.text("missing")).toBeUndefined();
    expect(doc.delete("cloud.yaml")).toBe(true);
    expect(doc.get("cloud.yaml")).toBeUndefined();
  });

  it("refuses paths leaving the root", () => {
    for (const bad of ["", "/etc/passwd", "..", "../x", "a/../../x", "."]) {
      expect(() => new Document().set(bad, "x")).toThrow(/stay inside the config root/);
    }
  });
});
