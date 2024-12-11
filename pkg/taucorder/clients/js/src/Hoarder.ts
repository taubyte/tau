import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./HoarderClient";
import { Node, Empty } from "../gen/taucorder/v1/common_pb";
import { StashedItem, StashRequest } from "../gen/taucorder/v1/hoarder_pb";

export class Hoarder {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List all stashed items
   * @returns AsyncIterable of stashed items
   */
  async list(): Promise<AsyncIterable<StashedItem>> {
    return this.client.list(this.node);
  }

  /**
   * Stash an item
   * @param cid Content ID to stash
   * @param providers Optional list of providers
   * @returns Empty response
   */
  async stash(cid: string, providers?: Node[]): Promise<Empty> {
    const request = new StashRequest({
      node: this.node,
      cid: cid,
      providers: providers,
    });
    return this.client.stash(request);
  }
} 