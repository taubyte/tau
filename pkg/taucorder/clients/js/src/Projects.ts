import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./ProjectsInAuthClient";
import { Node } from "../gen/taucorder/v1/common_pb";
import { Project, ByProjectRequest } from "../gen/taucorder/v1/auth_pb";

export class Projects {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List all projects
   * @returns AsyncIterable of projects
   */
  async list(): Promise<AsyncIterable<Project>> {
    return this.client.list(this.node);
  }

  /**
   * Get a specific project
   * @param projectId Project ID
   * @returns Project
   */
  async get(projectId: string): Promise<Project> {
    const request = new ByProjectRequest({
      node: this.node,
      id: projectId,
    });
    return this.client.get(request);
  }
} 