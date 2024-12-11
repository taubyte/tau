import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { MonkeyService } from "../gen/taucorder/v1/monkey_connect";
import { Node, Job } from "../gen/taucorder/v1/common_pb";
import { GetJobInstanceRequest } from "../gen/taucorder/v1/monkey_pb";

export class RPCClient {
  private client: PromiseClient<typeof MonkeyService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(MonkeyService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<Job>> {
    return this.client.list(req);
  }

  async get(req: GetJobInstanceRequest): Promise<Job> {
    return this.client.get(req);
  }
} 