import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { AuthService } from "../gen/taucorder/v1/auth_connect";
import {
  Node,
  Peer,
  DiscoverServiceRequest,
  ConsensusStateRequest,
  ConsensusState
} from "../gen/taucorder/v1/common_pb";

export class RPCClient {
  private client: PromiseClient<typeof AuthService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(AuthService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<Peer>> {
    return this.client.list(req);
  }

  async discover(req: DiscoverServiceRequest): Promise<AsyncIterable<Peer>> {
    return this.client.discover(req);
  }

  async state(req: ConsensusStateRequest): Promise<ConsensusState> {
    return this.client.state(req);
  }

  async states(req: Node): Promise<AsyncIterable<ConsensusState>> {
    return this.client.states(req);
  }
}
