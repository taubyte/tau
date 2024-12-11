import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./TNSClient";
import { Node, ConsensusState, ConsensusStateRequest } from "../gen/taucorder/v1/common_pb";
import { TNSListRequest, TNSFetchRequest, TNSLookupRequest, TNSPath, TNSObject, TNSPaths } from "../gen/taucorder/v1/tns_pb";

export class TNS {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List TNS paths
   * @param depth Depth of paths to list
   * @returns AsyncIterable of paths
   */
  async list(depth: number = 0): Promise<AsyncIterable<string[]>> {
    const request = new TNSListRequest({
      node: this.node,
      depth: depth
    });
    const paths = await this.client.list(request);
    return this.transformPaths(paths);
  }

  /**
   * Fetch TNS object
   * @param path Path to fetch
   * @returns TNS object data as JSON string
   */
  async fetch(path: string[]): Promise<string> {
    const request = new TNSFetchRequest({
      node: this.node,
      path: new TNSPath({
        leafs: path
      })
    });
    const response = await this.client.fetch(request);
    return response.json;
  }

  /**
   * Lookup TNS paths by prefix or regex
   * @param path Path to match
   * @param type 'prefix' or 'regex'
   * @returns Array of matched paths
   */
  async lookup(path: string[], type: 'prefix' | 'regex' = 'prefix'): Promise<string[][]> {
    const tnsPath = new TNSPath({
      leafs: path
    });

    const request = new TNSLookupRequest({
      node: this.node,
      match: {
        case: type,
        value: tnsPath
      }
    });

    const response = await this.client.lookup(request);
    return response.paths.map(p => p.leafs);
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

  private async *transformPaths(paths: AsyncIterable<TNSPath>): AsyncIterable<string[]> {
    for await (const path of paths) {
      yield path.leafs;
    }
  }
} 