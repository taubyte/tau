import {
  PromiseClient,
  createPromiseClient,
  Transport,
} from "@connectrpc/connect";
import { HealthService } from "../gen/health/v1/health_connect";
import { SupportsRequest, Empty } from "../gen/health/v1/health_pb";

export class RPCClient {
  private client: PromiseClient<typeof HealthService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(HealthService, transport);
  }

  async ping(req: Empty): Promise<Empty> {
    return this.client.ping(req);
  }

  async supports(req: SupportsRequest): Promise<Empty> {
    return this.client.supports(req);
  }
}
