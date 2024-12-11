import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { GitHooksInAuthService } from "../gen/taucorder/v1/auth_connect";
import { Node } from "../gen/taucorder/v1/common_pb";
import { Hook, ByHookRequest } from "../gen/taucorder/v1/auth_pb";

export class RPCClient {
  private client: PromiseClient<typeof GitHooksInAuthService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(GitHooksInAuthService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<Hook>> {
    return this.client.list(req);
  }

  async get(req: ByHookRequest): Promise<Hook> {
    return this.client.get(req);
  }
} 