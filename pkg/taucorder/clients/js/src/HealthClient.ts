import { PromiseClient, createPromiseClient, Transport } from "@connectrpc/connect";
import { HealthService } from "../gen/taucorder/v1/health_connect";
import { Empty } from "../gen/taucorder/v1/common_pb";

export class RPCClient {
  private client: PromiseClient<typeof HealthService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(HealthService, transport);
  }

  async ping(req: Empty): Promise<Empty> {
    return this.client.ping(req);
  }
} 