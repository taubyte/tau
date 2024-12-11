import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { NodeService } from "../gen/taucorder/v1/node_connect";
import { Node, Empty } from "../gen/taucorder/v1/common_pb";
import { Config } from "../gen/taucorder/v1/node_pb";

export class RPCClient {
  private client: PromiseClient<typeof NodeService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(NodeService, transport);
  }

  async new(req: Config): Promise<Node> {
    return this.client.new(req);
  }

  async free(req: Node): Promise<Empty> {
    return this.client.free(req);
  }
} 