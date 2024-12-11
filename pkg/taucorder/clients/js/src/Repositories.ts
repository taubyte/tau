import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./RepositoriesInAuthClient";
import { Node, RepositoryId } from "../gen/taucorder/v1/common_pb";
import { ProjectRepo, ByRepositoryRequest } from "../gen/taucorder/v1/auth_pb";

export class Repositories {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List all repositories
   * @returns AsyncIterable of repositories
   */
  async list(): Promise<AsyncIterable<ProjectRepo>> {
    return this.client.list(this.node);
  }

  /**
   * Get a specific repository
   * @param repoId Repository ID
   * @returns Repository
   */
  async get(repoId: string): Promise<ProjectRepo> {
    const request = new ByRepositoryRequest({
      node: this.node,
      id: new RepositoryId({ id: {case:"github", value:BigInt(repoId)} }),
    });
    return this.client.get(request);
  }
}
