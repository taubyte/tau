import { PromiseClient, createPromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";

import { DriveService } from "../gen/drive/v2/drive_connect";
import { Course, PlanRequest, Progress } from "../gen/drive/v2/drive_pb";
import { Document } from "./Document";

type TauBinarySource =
  | { value: boolean; case: "latest" }
  | { value: string; case: "version" }
  | { value: string; case: "url" }
  | { value: string; case: "path" };

const TauLatest: TauBinarySource = { value: true, case: "latest" };
const TauVersion = (version: string): TauBinarySource => ({ value: version, case: "version" });
const TauUrl = (url: string): TauBinarySource => ({ value: url, case: "url" });
const TauPath = (path: string): TauBinarySource => ({ value: path, case: "path" });

export { TauBinarySource, TauLatest, TauVersion, TauUrl, TauPath };

export interface PlanOptions {
  // tau is the binary every host is given; omitted is the latest release.
  tau?: TauBinarySource;
  // shapes are deployed in this order, one hop each; empty is every shape.
  shapes?: string[];
  // concurrency is how many hosts of one hop deploy at once; 0 is the default.
  concurrency?: number;
  // root is where tau lives on every host (bin, config, storage); omitted is /tb.
  root?: string;
}

export interface Validation {
  errors: string[];
  ignored: string[];
  // document is the normalized form, with generated secrets written in.
  // Keep it (Document.toDir) or the next validate generates different ones.
  document: Document;
}

// Drive talks to a spore-drive service at baseUrl.
export class Drive {
  private client: PromiseClient<typeof DriveService>;

  constructor(baseUrl: string) {
    this.client = createPromiseClient(
      DriveService,
      createConnectTransport({ baseUrl, httpVersion: "1.1" })
    );
  }

  async validate(doc: Document): Promise<Validation> {
    const res = await this.client.validate({ document: doc.toMessage() });
    return {
      errors: res.errors,
      ignored: res.ignored,
      document: Document.fromMessage(res.document),
    };
  }

  // plan refuses a document missing generated secrets: validate it first.
  async plan(doc: Document, options: PlanOptions = {}): Promise<Course> {
    return await this.client.plan(
      new PlanRequest({
        document: doc.toMessage(),
        tau: options.tau ?? TauLatest,
        shapes: options.shapes ?? [],
        concurrency: options.concurrency ?? 0,
        root: options.root ?? "",
      })
    );
  }

  // apply runs the course; it ends with the stream. Breaking out of the loop
  // (or passing an aborted signal) disconnects, which aborts the course.
  apply(course: Course | string, signal?: AbortSignal): AsyncIterable<Progress> {
    return this.client.apply({ course: idOf(course) }, { signal });
  }

  async abort(course: Course | string): Promise<void> {
    await this.client.abort({ course: idOf(course) });
  }
}

function idOf(course: Course | string): string {
  return typeof course === "string" ? course : course.id;
}
