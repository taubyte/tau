import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { PatrickService } from "../gen/taucorder/v1/patrick_connect";
import { Node, Job, ConsensusState, ConsensusStateRequest } from "../gen/taucorder/v1/common_pb";
import { GetJobRequest } from "../gen/taucorder/v1/patrick_pb";

export class RPCClient {
  private client: PromiseClient<typeof PatrickService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(PatrickService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<Job>> {
    return this.client.list(req);
  }

  async get(req: GetJobRequest): Promise<Job> {
    return this.client.get(req);
  }

  async state(req: ConsensusStateRequest): Promise<ConsensusState> {
    return this.client.state(req);
  }

  async states(req: Node): Promise<AsyncIterable<ConsensusState>> {
    return this.client.states(req);
  }
} 