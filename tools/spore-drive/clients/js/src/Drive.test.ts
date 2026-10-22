import { PromiseClient, createPromiseClient, ConnectError, Code } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { exec, ChildProcess } from "child_process";
import * as fs from "fs/promises";
import * as os from "os";
import * as path from "path";
import * as yaml from "js-yaml";
import * as tar from "tar";

import { MockSSHService } from "../gen/mock/v1/ssh_connect";
import { HostConfig, Host as Hostname } from "../gen/mock/v1/ssh_pb";
import { Document } from "./Document";
import { Drive, TauPath, TauLatest, TauVersion, TauUrl } from "./Drive";

const FAKE_TAU = "/tmp/faketau";

// startMock runs the Go mock (DriveService + MockSSHService) and resolves to
// its base URL.
export function startMock(dir: string, signal: AbortSignal): Promise<{ url: string; proc: ChildProcess }> {
  return new Promise((resolve, reject) => {
    const proc = exec(`cd ${dir}; go run .`, { signal });
    let done = false;
    proc.stdout?.on("data", (d: string) => {
      const m = String(d).match(/http:\/\/localhost:\d+\//);
      if (m && !done) {
        done = true;
        resolve({ url: m[0], proc });
      }
    });
    proc.on("error", (e) => {
      if (!done) reject(e);
    });
  });
}

// disk extracts a mock host's filesystem into a fresh directory and returns it.
export async function disk(mock: PromiseClient<typeof MockSSHService>, name: string): Promise<string> {
  const chunks: Uint8Array[] = [];
  for await (const c of mock.filesystem(new Hostname({ name }))) chunks.push(c.data);
  const out = await fs.mkdtemp(path.join(os.tmpdir(), "disk-"));
  const bundle = path.join(out, ".bundle.tar");
  await fs.writeFile(bundle, Buffer.concat(chunks));
  await tar.x({ file: bundle, cwd: out });
  await fs.rm(bundle);
  return out;
}

// configDir writes a minimal v2 cloud config whose hosts ssh into mock ports.
export async function configDir(dir: string, ports: Record<string, number>): Promise<void> {
  const hosts: Record<string, any> = {};
  for (const [name, port] of Object.entries(ports)) {
    hosts[name] = {
      addr: ["127.0.0.1/32"],
      ssh: { addr: "127.0.0.1", port, auth: ["main"] },
      shapes: { shape1: {} },
    };
  }
  const files: Record<string, any> = {
    "cloud.yaml": {
      domain: { root: "test.com", generated: "gtest.com" },
      p2p: { bootstrap: { shape1: Object.keys(ports) } },
      auth: {
        admin: { name: "root", password: "correct-horse" },
        forges: [{ host: "github.com", driver: "github", clientid: "gh-id", clientsecret: "gh-secret" }],
      },
      cm: { acme: { accountkey: "keys/acme.key" } },
    },
    "shapes.yaml": { shape1: { services: ["auth", "seer"], ports: { main: 4242 } } },
    "hosts.yaml": hosts,
    "auth.yaml": { main: { username: "tau1", password: "testtest" } },
    "added.yaml": { example: { greeting: "hello", replicas: 2 } },
    "tenancy.yaml": { provider: "github", owner: "Acme" },
  };
  for (const [name, content] of Object.entries(files)) {
    await fs.writeFile(path.join(dir, name), yaml.dump(content));
  }
  await fs.mkdir(path.join(dir, "keys"), { recursive: true });
  await fs.writeFile(path.join(dir, "keys/acme.key"), "ACME ACCOUNT KEY");
}

describe("Drive against the mock", () => {
  const controller = new AbortController();
  let mockProc: ChildProcess;
  let mock: PromiseClient<typeof MockSSHService>;
  let drive: Drive;
  let dir: string;
  let names: string[] = [];
  let run = 0;

  beforeAll(async () => {
    const { url, proc } = await startMock("../mock", controller.signal);
    mockProc = proc;
    mock = createPromiseClient(MockSSHService, createConnectTransport({ baseUrl: url, httpVersion: "1.1" }));
    drive = new Drive(url);
    await fs.writeFile(FAKE_TAU, "");
  });

  afterAll(async () => {
    controller.abort();
    await new Promise((r) => mockProc.on("close", r));
  });

  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), "cloud-"));
    const ports: Record<string, number> = {};
    run++;
    names = [`host${run}a`, `host${run}b`];
    for (const name of names) {
      const h = await mock.new(
        new HostConfig({ host: new Hostname({ name }), authUsername: "tau1", authPassword: "testtest" })
      );
      ports[name] = h.port;
    }
    await configDir(dir, ports);
  });

  afterEach(async () => {
    for (const name of names) await mock.free(new Hostname({ name }));
    await fs.rm(dir, { recursive: true, force: true });
  });

  async function validated(): Promise<Document> {
    const v = await drive.validate(await Document.fromDir(dir));
    expect(v.errors).toEqual([]);
    await v.document.toDir(dir);
    return Document.fromDir(dir);
  }

  it("validates, plans and applies", async () => {
    const doc = await validated();
    for (const k of ["keys/ca.crt", "keys/ca.key"]) {
      expect(doc.get(k)?.length).toBeGreaterThan(0);
    }
    const hosts = yaml.load(doc.text("hosts.yaml")!) as any;
    expect(hosts[names[0]].shapes.shape1.key).toBeTruthy();

    const course = await drive.plan(doc, { tau: TauPath(FAKE_TAU), shapes: ["shape1"], concurrency: 1, root: "/opt/tau" });
    expect(course.id).toBeTruthy();
    expect(course.hops.map((h) => h.shapes)).toEqual([["shape1"]]);
    expect(course.hosts.map((h) => h.name).sort()).toEqual(names);
    expect(course.hosts[0].nodes[0].port).toBe(4242);

    const seen = new Set<string>();
    const errors: string[] = [];
    for await (const p of drive.apply(course)) {
      if (p.host) seen.add(p.host);
      if (p.error) errors.push(`${p.host}/${p.step}: ${p.error}`);
    }
    expect(errors).toEqual([]);
    expect(seen).toEqual(new Set(names));

    const cmds = new Set<string>();
    for await (const c of mock.commands(new Hostname({ name: names[0] }))) cmds.add(c.command);
    for (const c of ['command "-v" "systemctl"', 'command "-v" "docker"', 'sudo "apt-get" "update"']) {
      expect(cmds).toContain(c);
    }
    // The node's boot file was uploaded and installed under the root, its
    // secrets handed to init on stdin, and the node, named for its primary
    // shape, started.
    expect(cmds).toContain("SFTP Filewrite /tmp/shape1.yaml");
    expect(cmds).toContain('sudo "cp" "-f" "/tmp/shape1.yaml" "/opt/tau/config/"');
    expect(cmds).toContain(
      'sudo "/opt/tau/bin/tau" "init" "--config" "/opt/tau/config/shape1.yaml" "--root" "/opt/tau/storage/shape1" "-"'
    );
    expect(cmds).toContain('sudo "systemctl" "start" "tau@shape1"');
    for (const c of cmds) expect(c).not.toContain("gh-secret");

    // One sealed bundle reaches every host's init on stdin, byte for byte the
    // same, with the secret sealed in it; no other command is handed stdin.
    const bundles: Buffer[] = [];
    for (const name of names) {
      for await (const c of mock.commands(new Hostname({ name }))) {
        if (c.command.includes('"init"')) bundles.push(Buffer.from(c.stdin));
        else expect(c.stdin.length).toBe(0);
      }
    }
    expect(bundles).toHaveLength(names.length);
    expect(bundles[0].length).toBeGreaterThan(0);
    for (const b of bundles) expect(b.equals(bundles[0])).toBe(true);
    expect(bundles[0].includes("oauth-github.com")).toBe(true);
    expect(bundles[0].includes("admin-password")).toBe(true);
    for (const secret of ["correct-horse", "gh-secret", "ACME ACCOUNT KEY"]) {
      expect(bundles[0].includes(secret)).toBe(false);
    }

    // What was installed is the node the config describes, read off the host.
    const root = await disk(mock, names[0]);
    const raw = await fs.readFile(path.join(root, "opt/tau/config/shape1.yaml"), "utf8");
    const node = yaml.load(raw) as any;
    const onDisk: string[] = [];
    for (const entry of await fs.readdir(root, { recursive: true, withFileTypes: true })) {
      if (entry.isFile()) onDisk.push(await fs.readFile(path.join(entry.parentPath, entry.name), "utf8"));
    }
    await fs.rm(root, { recursive: true, force: true });
    expect(node.node.services).toEqual(["auth", "secrets", "discovery", "dns"]);
    expect(node.node.cloud).toBe("test.com");
    expect(node.node.tenancy).toEqual(["github.com/acme"]);
    expect(node.node.added).toEqual({ example: { greeting: "hello", replicas: 2 } });
    expect(node.ca).toContain("BEGIN CERTIFICATE");
    expect(node.certificate).toContain("BEGIN CERTIFICATE");
    expect(node.privatekey).toBeTruthy();
    expect(node.listen).toEqual(["/ip4/0.0.0.0/tcp/4242"]);
    expect(node.bootstrap).toHaveLength(1);
    // No domain key reaches a node: none signs a domain claim.
    expect(raw).not.toContain("PRIVATE KEY");
    // Sign-in and certificates reach the node; their secrets no file at all.
    expect(node.node.auth.admin).toEqual({ name: "root", disabled: false });
    expect(node.node.auth.forges).toEqual([{ host: "github.com", clientid: "gh-id" }]);
    expect(node.node.cm.certificate.acme.enabled).toBe(true);
    for (const secret of ["correct-horse", "$argon2id$", "gh-secret", "ACME ACCOUNT KEY"]) {
      for (const content of onDisk) expect(content).not.toContain(secret);
    }

    // The course ended with the stream.
    await expect(drive.abort(course)).rejects.toMatchObject({ code: Code.NotFound });
  });

  it("aborts an unapplied course", async () => {
    const course = await drive.plan(await validated(), { tau: TauPath(FAKE_TAU) });
    await drive.abort(course.id);
    await expect(drive.abort(course)).rejects.toBeInstanceOf(ConnectError);
  });

  it("refuses to plan an incomplete document", async () => {
    const err = await drive.plan(await Document.fromDir(dir)).catch((e) => e);
    expect(err).toBeInstanceOf(ConnectError);
    expect(err.code).toBe(Code.FailedPrecondition);
    expect(err.message).toMatch(/the cloud.s CA/);
  });

  it("lists errors without failing", async () => {
    const doc = await Document.fromDir(dir);
    doc.set("shapes.yaml", "shape1:\n  services: [nope]\n  ports:\n    main: 4242\n");
    const v = await drive.validate(doc);
    expect(v.errors.length).toBeGreaterThan(0);
  });
});

describe("tau sources", () => {
  it("builds each oneof case", () => {
    expect(TauLatest).toEqual({ case: "latest", value: true });
    expect(TauVersion("v1")).toEqual({ case: "version", value: "v1" });
    expect(TauUrl("u")).toEqual({ case: "url", value: "u" });
    expect(TauPath("p")).toEqual({ case: "path", value: "p" });
  });
});
