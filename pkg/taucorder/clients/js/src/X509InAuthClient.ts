import { PromiseClient, createPromiseClient,Transport } from "@connectrpc/connect";
import { X509InAuthService } from "../gen/taucorder/v1/auth_connect";
import { Node, Empty } from "../gen/taucorder/v1/common_pb";
import { X509Certificate, X509CertificateRequest } from "../gen/taucorder/v1/auth_pb";

export class RPCClient {
  private client: PromiseClient<typeof X509InAuthService>;

  constructor(transport: Transport) {
    this.client = createPromiseClient(X509InAuthService, transport);
  }

  async list(req: Node): Promise<AsyncIterable<X509Certificate>> {
    return this.client.list(req);
  }

  async get(req: X509CertificateRequest): Promise<X509Certificate> {
    return this.client.get(req);
  }

  async set(req: X509CertificateRequest): Promise<Empty> {
    return this.client.set(req);
  }

  async delete(req: X509CertificateRequest): Promise<Empty> {
    return this.client.delete(req);
  }
} 