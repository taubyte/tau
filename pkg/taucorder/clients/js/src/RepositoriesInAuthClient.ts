import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { RepositoriesInAuthService } from "../gen/taucorder/v1/auth_connect";
import { Node } from "../gen/taucorder/v1/common_pb";
import { ProjectRepo, ByRepositoryRequest } from "../gen/taucorder/v1/auth_pb";

export class RPCClient {
  private client: PromiseClient<typeof RepositoriesInAuthService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(RepositoriesInAuthService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<ProjectRepo>> {
    return this.client.list(req);
  }

  async get(req: ByRepositoryRequest): Promise<ProjectRepo> {
    return this.client.get(req);
  }
} 