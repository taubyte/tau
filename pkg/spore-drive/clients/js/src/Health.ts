import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./HealthClient";
import { SupportsRequest, Empty } from "../gen/health/v1/health_pb";

export class Health {
  private client: RPCClient;

  constructor(transport: Transport) {
    this.client = new RPCClient(transport);
  }

  /**
   * Ping the health service
   * @returns Empty response
   * @throws Error if the server does not respond
   */
  async ping() {
    await this.client.ping(new Empty());
  }

  /**
   * Check if the server supports a given version
   * @param version - The version to check
   * @returns Empty response
   * @throws Error if the server does not support the given version
   */
  async supports(version: string) {
    return this.client.supports(new SupportsRequest({ version }));
  }
}
