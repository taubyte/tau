import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { SwarmService } from "../gen/taucorder/v1/swarm_connect";
import { Empty, Peer } from "../gen/taucorder/v1/common_pb";
import { WaitRequest, ListRequest, PingRequest, ConnectRequest, DiscoverRequest } from "../gen/taucorder/v1/swarm_pb";

export class RPCClient {
  private client: PromiseClient<typeof SwarmService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(SwarmService, transport);
  }

  async wait(req: WaitRequest): Promise<Empty> {
    return this.client.wait(req);
  }

  async list(req: ListRequest): Promise<AsyncIterable<Peer>> {
    return this.client.list(req);
  }

  async ping(req: PingRequest): Promise<Peer> {
    return this.client.ping(req);
  }

  async connect(req: ConnectRequest): Promise<Peer> {
    return this.client.connect(req);
  }

  async discover(req: DiscoverRequest): Promise<AsyncIterable<Peer>> {
    return this.client.discover(req);
  }
} 