import {
  PromiseClient,
  createPromiseClient,
  Transport,
} from "@connectrpc/connect";
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
  // exposed so sibling-service clients (e.g. ee EnterpriseService) bind to the
  // SAME transport instead of opening a second connection.
  readonly transport: Transport;

  constructor(baseUrl: string) {
    this.transport = createConnectTransport({
      baseUrl: baseUrl,
      httpVersion: "1.1",
      defaultTimeoutMs: 3000,
    });
    this.client = createPromiseClient(ConfigService, this.transport);
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

  // do wraps the built inner op in a ConfigService Op and sends it. Implementing
  // OpClient this way lets the operation classes (StringOperation, etc.) stay
  // service-agnostic — an ee client can supply an OpClient that wraps a different
  // service's op message instead.
  async do(config: Config, innerOp: any): Promise<Return> {
    return await this.client.do(new Op({ config, op: innerOp }));
  }
}

// OpClient is the minimal surface the operation classes need: wrap a built inner
// op for a given config and dispatch it. RPCClient implements it for
// ConfigService; ee clients implement it for their own service.
export interface OpClient {
  do(config: Config, innerOp: any): Promise<Return>;
}
