import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./HealthClient";
import { Empty, Node } from "../gen/taucorder/v1/common_pb";

export class Health {
  private client: RPCClient;

  constructor(transport: Transport) {
    this.client = new RPCClient(transport);
  }

  /**
   * Ping the health service
   * @returns Empty response
   */
  async ping() {
    await this.client.ping(new Empty());
  }
} 