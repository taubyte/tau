import { PromiseClient, createPromiseClient } from "@connectrpc/connect";
import { DriveService } from "../gen/drive/v1/drive_connect";
import { createConnectTransport } from "@connectrpc/connect-node";

import {
  DriveRequest,
  Empty,
  PlotRequest,
  Drive,
  Course,
  DisplacementProgress,
} from "../gen/drive/v1/drive_pb";

export class RPCClient {
  private client: PromiseClient<typeof DriveService>;

  constructor(baseUrl: string) {
    const transport = createConnectTransport({
      baseUrl: baseUrl,
      httpVersion: "1.1",
    });
    this.client = createPromiseClient(DriveService, transport);
  }

  async new(driveRequest: DriveRequest): Promise<Drive> {
    return await this.client.new(driveRequest);
  }

  async plot(plotRequest: PlotRequest): Promise<Course> {
    return await this.client.plot(plotRequest);
  }

  async displace(course: Course): Promise<Empty> {
    return await this.client.displace(course);
  }

  async progress(course: Course): Promise<AsyncIterable<DisplacementProgress>> {
    return await this.client.progress(course);
  }

  async abort(course: Course): Promise<Empty> {
    return await this.client.abort(course);
  }

  async free(drive: Drive): Promise<Empty> {
    return await this.client.free(drive);
  }
}
