import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./AuthClient";
import {
  Node,
  Peer,
  DiscoverServiceRequest,
  ConsensusStateRequest,
  ConsensusState,
} from "../gen/taucorder/v1/common_pb";

export class Auth {
  private client: RPCClient;
  protected node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List peers
   * @returns AsyncIterable of peers
   */
  async list(): Promise<AsyncIterable<Peer>> {
    return this.client.list(this.node);
  }

  /**
   * Discover peers with optional timeout and count limits
   * @param timeout Optional timeout in milliseconds
   * @param count Optional maximum number of peers to discover
   * @returns AsyncIterable of discovered peers
   */
  async discover(
    timeout?: number,
    count?: number
  ): Promise<AsyncIterable<Peer>> {
    const request = new DiscoverServiceRequest({
      node: this.node,
      timeout: BigInt(timeout || 0),
      count: BigInt(count || 0),
    });
    return this.client.discover(request);
  }

  /**
   * Get consensus state for a specific peer
   * @param peerId Peer ID
   * @returns Consensus state
   */
  async state(peerId: string): Promise<ConsensusState> {
    const request = new ConsensusStateRequest({
      node: this.node,
      pid: peerId,
    });
    return this.client.state(request);
  }

  /**
   * Get consensus states for all peers
   * @returns AsyncIterable of consensus states
   */
  async states(): Promise<AsyncIterable<ConsensusState>> {
    return this.client.states(this.node);
  }
}
