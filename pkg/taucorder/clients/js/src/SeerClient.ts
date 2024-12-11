import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { SeerService } from "../gen/taucorder/v1/seer_connect";
import { Peer } from "../gen/taucorder/v1/common_pb";
import { NodesListRequest, NodesUsageRequest, LocationRequest, PeerUsage, PeerLocation } from "../gen/taucorder/v1/seer_pb";

export class RPCClient {
  private client: PromiseClient<typeof SeerService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(SeerService, transport);
  }

  async list(req: NodesListRequest): Promise<AsyncIterable<Peer>> {
    return this.client.list(req);
  }

  async usage(req: NodesUsageRequest): Promise<PeerUsage> {
    return this.client.usage(req);
  }

  async location(req: LocationRequest): Promise<AsyncIterable<PeerLocation>> {
    return this.client.location(req);
  }
} 