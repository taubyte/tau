import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./GitHooksInAuthClient";
import { Node } from "../gen/taucorder/v1/common_pb";
import { Hook, ByHookRequest } from "../gen/taucorder/v1/auth_pb";

export class GitHooks {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List all hooks
   * @returns AsyncIterable of hooks
   */
  async list(): Promise<AsyncIterable<Hook>> {
    return this.client.list(this.node);
  }

  /**
   * Get a specific hook
   * @param hookId Hook ID
   * @returns Hook
   */
  async get(hookId: string): Promise<Hook> {
    const request = new ByHookRequest({
      node: this.node,
      id: hookId,
    });
    return this.client.get(request);
  }
} 