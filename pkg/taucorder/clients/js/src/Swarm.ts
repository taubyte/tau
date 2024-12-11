import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./SwarmClient";
import { Empty, Node, Peer } from "../gen/taucorder/v1/common_pb";
import { WaitRequest, ListRequest, PingRequest, ConnectRequest, DiscoverRequest, ListPingRequest } from "../gen/taucorder/v1/swarm_pb";

export class Swarm {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * Wait for swarm readiness
   * @param timeout Timeout in nanoseconds
   * @returns Empty response
   */
  async wait(timeout?: bigint): Promise<Empty> {
    const request = new WaitRequest({
      node: this.node,
      timeout: timeout ?? BigInt(0)
    });
    return this.client.wait(request);
  }

  /**
   * List peers
   * @param options.timeout_seconds Timeout in seconds (optional)
   * @param options.ping.count Number of pings to send (optional)
   * @param options.ping.concurrency Ping concurrency level (optional)
   * @returns AsyncIterable of peers
   */
  async list(options?: {
    timeout_seconds?: number;
    ping?: {
      count: number;
      concurrency?: number;
    };
  }): Promise<AsyncIterable<Peer>> {
    const request = new ListRequest({
      node: this.node,
      timeout: options?.timeout_seconds 
        ? BigInt(Math.floor(options.timeout_seconds * 1_000_000_000)) 
        : BigInt(0),
      ping: options?.ping 
        ? new ListPingRequest({
            count: options.ping.count,
            concurrency: options.ping.concurrency ?? 0
          })
        : undefined
    });
    return this.client.list(request);
  }

  /**
   * Ping a peer
   * @param peerId Peer ID to ping
   * @param options.timeout_seconds Timeout in seconds (optional)
   * @param options.count Number of pings to send (optional)
   * @returns Peer
   */
  async ping(peerId: string, options?: {
    timeout_seconds?: number;
    count?: number;
  }): Promise<Peer> {
    const request = new PingRequest({
      node: this.node,
      pid: peerId,
      timeout: options?.timeout_seconds 
      ? BigInt(Math.floor(options.timeout_seconds * 1_000_000_000)) 
      : BigInt(0),
      count: options?.count ?? 0
    });
    return this.client.ping(request);
  }

  /**
   * Connect to a peer
   * @param address Peer address to connect to
   * @param timeout Timeout in nanoseconds
   * @returns Peer
   */
  async connect(address: string, timeout?: bigint): Promise<Peer> {
    const request = new ConnectRequest({
      node: this.node,
      address,
      timeout: timeout ?? BigInt(0)
    });
    return this.client.connect(request);
  }

  /**
   * Discover peers
   * @param service Service to discover
   * @param options.timeout_seconds Timeout in seconds (optional)
   * @param options.count Maximum number of peers to discover (optional)
   * @returns AsyncIterable of peers
   */
  async discover(service: string, options?: {
    timeout_seconds?: number;
    count?: number;
  }): Promise<AsyncIterable<Peer>> {
    const request = new DiscoverRequest({
      node: this.node,
      service,
      timeout: options?.timeout_seconds
        ? BigInt(Math.floor(options.timeout_seconds * 1_000_000_000))
        : BigInt(0),
      count: options?.count ?? 0
    });
    return this.client.discover(request);
  }
} 