import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { ProjectsInAuthService } from "../gen/taucorder/v1/auth_connect";
import { Node } from "../gen/taucorder/v1/common_pb";
import { Project, ByProjectRequest } from "../gen/taucorder/v1/auth_pb";

export class RPCClient {
  private client: PromiseClient<typeof ProjectsInAuthService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(ProjectsInAuthService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<Project>> {
    return this.client.list(req);
  }

  async get(req: ByProjectRequest): Promise<Project> {
    return this.client.get(req);
  }
} 