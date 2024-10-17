import { PromiseClient, createPromiseClient } from "@connectrpc/connect";
import { ConfigService } from "../gen/config/v1/config_connect";
import { createConnectTransport } from "@connectrpc/connect-node";

import {
  Source,
  Empty,
  Config,
  Bundle,
  BundleConfig,
  Op,
  Return,
  SourceUpload,
} from "../gen/config/v1/config_pb";

export class RPCClient {
  private client: PromiseClient<typeof ConfigService>;

  constructor(baseUrl: string) {
    const transport = createConnectTransport({
      baseUrl: baseUrl,
      httpVersion: "1.1",
      defaultTimeoutMs: 3000,
    });
    this.client = createPromiseClient(ConfigService, transport);
  }

  async new(): Promise<Config> {
    return await this.client.new(new Empty());
  }

  async load(source: Source): Promise<Config> {
    return await this.client.load(source);
  }

  async upload(sourceUploads: AsyncIterable<SourceUpload>): Promise<Config> {
    return await this.client.upload(sourceUploads);
  }

  async download(bundleConfig: BundleConfig): Promise<AsyncIterable<Bundle>> {
    return await this.client.download(bundleConfig);
  }

  async commit(config: Config): Promise<Empty> {
    return await this.client.commit(config);
  }

  async free(config: Config): Promise<Empty> {
    return await this.client.free(config);
  }

  async do(op: Op): Promise<Return> {
    return await this.client.do(op);
  }
}
