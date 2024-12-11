import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./X509InAuthClient";
import { Node, Empty } from "../gen/taucorder/v1/common_pb";
import { X509Certificate, X509CertificateRequest } from "../gen/taucorder/v1/auth_pb";

export class X509 {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List X509 certificates
   * @returns AsyncIterable of X509 certificates
   */
  async list(): Promise<AsyncIterable<X509Certificate>> {
    return this.client.list(this.node);
  }

  /**
   * Get X509 certificate
   * @param certId Certificate ID
   * @returns X509 certificate
   */
  async get(certId: string): Promise<X509Certificate> {
    const request = new X509CertificateRequest({
      node: this.node,
      domain: certId,
    });
    return this.client.get(request);
  }

  /**
   * Set X509 certificate
   * @param certId Certificate ID
   * @returns Empty response
   */
  async set(certId: string, data: Uint8Array): Promise<Empty> {
    const request = new X509CertificateRequest({
      node: this.node,
      domain: certId,
      data: data,
    });
    return this.client.set(request);
  }

  /**
   * Delete X509 certificate
   * @param certId Certificate ID
   * @returns Empty response
   */
  async delete(certId: string): Promise<Empty> {
    const request = new X509CertificateRequest({
      node: this.node,
      domain: certId,
    });
    return this.client.delete(request);
  }
} 