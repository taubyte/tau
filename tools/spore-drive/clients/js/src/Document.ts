import * as fs from "fs/promises";
import * as path from "path";

import { Document as DocumentMessage } from "../gen/drive/v2/drive_pb";

export type FileContent = string | Uint8Array;

// Document is a cloud config directory held in memory: every file by its path
// relative to the config root ("cloud.yaml", "keys/swarm.key", ...).
export class Document {
  readonly files: Map<string, Uint8Array>;

  constructor(files?: Record<string, FileContent> | Map<string, Uint8Array>) {
    this.files = new Map();
    if (files instanceof Map) {
      for (const [name, data] of files) this.set(name, data);
    } else if (files) {
      for (const name of Object.keys(files)) this.set(name, files[name]);
    }
  }

  // fromObject builds a document from in-memory file contents.
  static fromObject(files: Record<string, FileContent>): Document {
    return new Document(files);
  }

  // fromDir reads every regular file under dir.
  static async fromDir(dir: string): Promise<Document> {
    const doc = new Document();
    const walk = async (rel: string): Promise<void> => {
      for (const entry of await fs.readdir(path.join(dir, rel), { withFileTypes: true })) {
        const name = rel ? `${rel}/${entry.name}` : entry.name;
        if (entry.isDirectory()) await walk(name);
        else if (entry.isFile()) doc.set(name, await fs.readFile(path.join(dir, name)));
      }
    };
    await walk("");
    return doc;
  }

  static fromMessage(msg?: DocumentMessage): Document {
    return new Document(new Map(Object.entries(msg?.files ?? {})));
  }

  toMessage(): DocumentMessage {
    return new DocumentMessage({ files: Object.fromEntries(this.files) });
  }

  // toDir writes every file under dir, creating directories as needed.
  // Files already in dir that the document does not hold are left alone.
  async toDir(dir: string): Promise<void> {
    for (const [name, data] of this.files) {
      const target = path.join(dir, name);
      await fs.mkdir(path.dirname(target), { recursive: true });
      await fs.writeFile(target, data, { mode: 0o600 });
    }
  }

  set(name: string, data: FileContent): this {
    this.files.set(checkName(name), typeof data === "string" ? new TextEncoder().encode(data) : data);
    return this;
  }

  get(name: string): Uint8Array | undefined {
    return this.files.get(name);
  }

  text(name: string): string | undefined {
    const data = this.files.get(name);
    return data === undefined ? undefined : new TextDecoder().decode(data);
  }

  delete(name: string): boolean {
    return this.files.delete(name);
  }
}

// checkName refuses a path that would leave the config root.
function checkName(name: string): string {
  const clean = path.posix.normalize(name.replace(/\\/g, "/"));
  if (!name || clean.startsWith("/") || clean === "." || clean === ".." || clean.startsWith("../")) {
    throw new Error(`file path ${JSON.stringify(name)} must be relative and stay inside the config root`);
  }
  return clean;
}
