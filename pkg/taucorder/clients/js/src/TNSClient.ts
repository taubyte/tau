import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { TNSService } from "../gen/taucorder/v1/tns_connect";
import { Node, ConsensusState, ConsensusStateRequest } from "../gen/taucorder/v1/common_pb";
import { TNSListRequest, TNSFetchRequest, TNSLookupRequest, TNSPath, TNSObject, TNSPaths } from "../gen/taucorder/v1/tns_pb";

export class RPCClient {
  private client: PromiseClient<typeof TNSService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(TNSService, transport);
  }

  async list(req: TNSListRequest): Promise<AsyncIterable<TNSPath>> {
    return this.client.list(req);
  }

  async fetch(req: TNSFetchRequest): Promise<TNSObject> {
    return this.client.fetch(req);
  }

  async lookup(req: TNSLookupRequest): Promise<TNSPaths> {
    return this.client.lookup(req);
  }

  async state(req: ConsensusStateRequest): Promise<ConsensusState> {
    return this.client.state(req);
  }

  async states(req: Node): Promise<AsyncIterable<ConsensusState>> {
    return this.client.states(req);
  }
} 