import * as fs from "fs/promises";

import { Document, Drive, TauLatest } from "@taubyte/spore-drive";

// A cloud described in memory. The service validates it, generating every
// secret left out: the cloud's CA, the domain validation keys and each host's
// node key. Every node it deploys is issued a certificate from that CA, which
// is how the nodes admit each other.
const doc = Document.fromObject({
  "cloud.yaml": `
domain:
  root: test.com
  generated: gtest.com
p2p:
  bootstrap:
    shape1: [host1, host2]
`,
  "auth.yaml": `
main:
  username: tau1
  password: testtest
`,
  "shapes.yaml": `
shape1:
  services: [auth, secrets, dns, gateway]
  ports:
    main: 4242
`,
  "hosts.yaml": `
host1:
  addr: [1.2.3.4/24]
  ssh: {addr: 1.2.3.4, port: 22, auth: [main]}
  shapes: {shape1: {}}
host2:
  addr: [8.2.3.4/24]
  ssh: {addr: 8.2.3.4, port: 22, auth: [main]}
  shapes: {shape1: {}}
`,
});

// To use a CA you own instead of a generated one, give its root certificate
// and key (PKCS#8), both PEM, at the paths cloud.yaml names (keys/ca.crt and
// keys/ca.key by default).
if (process.env.CA_CERT && process.env.CA_KEY) {
  doc.set("keys/ca.crt", await fs.readFile(process.env.CA_CERT));
  doc.set("keys/ca.key", await fs.readFile(process.env.CA_KEY));
}

const drive = await Drive.local();

const { errors, document } = await drive.validate(doc);
if (errors.length) throw new Error(errors.join("\n"));
// Keep the normalized document: the next validate would generate a new CA and
// new node keys.
await document.toDir("./config");

const course = await drive.plan(document, { tau: TauLatest, shapes: ["shape1"] });
for await (const p of drive.apply(course)) {
  console.log(p.hop, p.host, p.step, p.progress, p.error ?? "");
}
console.log("done");
