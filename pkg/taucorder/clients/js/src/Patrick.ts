import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./PatrickClient";
import { Node, Job, ConsensusState, ConsensusStateRequest } from "../gen/taucorder/v1/common_pb";
import { GetJobRequest } from "../gen/taucorder/v1/patrick_pb";

export class Patrick {
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
   * Get a specific job
   * @param jobId Job ID
   * @returns Job
   */
  async get(jobId: string): Promise<Job> {
    const request = new GetJobRequest({
      node: this.node,
      id: jobId,
    });
    return this.client.get(request);
  }

  /**
   * Get consensus state
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
   * Get all consensus states
   * @returns AsyncIterable of consensus states
   */
  async states(): Promise<AsyncIterable<ConsensusState>> {
    return this.client.states(this.node);
  }
} 