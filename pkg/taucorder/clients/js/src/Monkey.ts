import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./MonkeyClient";
import { Node, Job } from "../gen/taucorder/v1/common_pb";
import { GetJobInstanceRequest } from "../gen/taucorder/v1/monkey_pb";

export class Monkey {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List all jobs
   * @returns AsyncIterable of jobs
   */
  async list(): Promise<AsyncIterable<Job>> {
    return this.client.list(this.node);
  }

  /**
   * Get a specific job instance
   * @param jobId Job ID
   * @returns Job
   */
  async get(jobId: string): Promise<Job> {
    const request = new GetJobInstanceRequest({
      node: this.node,
      id: jobId,
    });
    return this.client.get(request);
  }
} 