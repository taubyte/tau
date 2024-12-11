import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { HoarderService } from "../gen/taucorder/v1/hoarder_connect";
import { Node, Empty } from "../gen/taucorder/v1/common_pb";
import { StashedItem, StashRequest } from "../gen/taucorder/v1/hoarder_pb";

export class RPCClient {
  private client: PromiseClient<typeof HoarderService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(HoarderService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<StashedItem>> {
    return this.client.list(req);
  }

  async stash(req: StashRequest): Promise<Empty> {
    return this.client.stash(req);
  }
} 