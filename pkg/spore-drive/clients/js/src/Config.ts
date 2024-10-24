import { stringify } from "querystring";
import {
  Source,
  Op,
  Config as ConfigMessage,
  Empty,
  Return,
  StringSlice,
  BundleConfig,
  Bundle,
  SourceUpload
} from "../gen/config/v1/config_pb";

import { RPCClient } from "./ConfigClient";

async function* uploadAsyncIterator(stream: ReadableStream<Uint8Array>): AsyncIterable<SourceUpload> {
  yield new SourceUpload({data:{case:"path", value:"/"}});

  for await (const chunk of stream) {
    yield new SourceUpload({data: {case:"chunk", value:chunk}});
  }
}

export class Config {
  private client!: RPCClient;
  private source?: string | ReadableStream<Uint8Array>;
  private config?: ConfigMessage;

  constructor(source?: string | ReadableStream<Uint8Array>) {
    this.source = source;
  }

  async init(url: string): Promise<void> {
    this.client = new RPCClient(url);
    if (typeof this.source === 'string') {
      this.config = await this.client.load(new Source({ root: this.source, path: "/" }))
    } else if (this.source instanceof ReadableStream) {
      this.config = await this.client.upload(uploadAsyncIterator(this.source))
    } else {
      this.config = await this.client.new();
    }
  }

  async free(): Promise<void> {
    if (this.config) await this.client.free(this.config);
  }

  id(): string | undefined {
    return this.config?.id
  }

  Cloud() {
    return new Cloud(this.client, this.config!);
  }

  Hosts() {
    return new Hosts(this.client, this.config!);
  }

  Auth() {
    return new Auth(this.client, this.config!);
  }

  Shapes() {
    return new Shapes(this.client, this.config!);
  }

  async Commit(): Promise<Empty> {
    if (!this.config) throw new Error("Config not loaded.");
    return await this.client.commit(this.config);
  }

  async Download(): Promise<AsyncIterable<Bundle>> {
    return await this.client.download(new BundleConfig({ id: this.config }));
  }
}

// Base Operation class to hold the path
class BaseOperation {
  protected client: RPCClient;
  protected config: ConfigMessage;
  protected path: any[];

  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    this.client = client;
    this.config = config;
    this.path = path;
  }

  protected async doRequest(operation: any): Promise<Return> {
    const op = this.buildOp(operation);
    const finalOp = new Op({
      config: this.config,
      op: op,
    });
    return await this.client.do(finalOp);
  }

  private buildOp(operation: any): any {
    let op = operation;
    for (let i = this.path.length - 1; i >= 0; i--) {
      const pathItem = this.path[i];
      const caseName = pathItem.case;
      const messageValue: any = { op };

      if (pathItem.name) {
        messageValue.name = pathItem.name;
      }
      if (pathItem.shape) {
        messageValue.shape = pathItem.shape;
      }

      op = { case: caseName, value: messageValue };
    }
    return op;
  }
}

// Cloud Operations
class Cloud extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage) {
    super(client, config, [{ case: "cloud" }]);
  }

  Domain() {
    return new Domain(this.client, this.config, [
      ...this.path,
      { case: "domain" },
    ]);
  }

  P2P() {
    return new P2P(this.client, this.config, [...this.path, { case: "p2p" }]);
  }
}

class Domain extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Root() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "root" },
    ]);
  }

  Generated() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "generated" },
    ]);
  }

  Validation() {
    return new Validation(this.client, this.config, [
      ...this.path,
      { case: "validation" },
    ]);
  }
}

class Validation extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Keys() {
    return new ValidationKeys(this.client, this.config, [
      ...this.path,
      { case: "keys" },
    ]);
  }

  async Generate(): Promise<void> {
    await this.doRequest({ case: "generate", value: true });
  }
}

class ValidationKeys extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Path() {
    return new ValidationKeysPath(this.client, this.config, [
      ...this.path,
      { case: "path" },
    ]);
  }

  Data() {
    return new ValidationKeysData(this.client, this.config, [
      ...this.path,
      { case: "data" },
    ]);
  }
}

class ValidationKeysPath extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  PrivateKey() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "privateKey" },
    ]);
  }

  PublicKey() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "publicKey" },
    ]);
  }
}

class ValidationKeysData extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  PrivateKey() {
    return new BytesOperation(this.client, this.config, [
      ...this.path,
      { case: "privateKey" },
    ]);
  }

  PublicKey() {
    return new BytesOperation(this.client, this.config, [
      ...this.path,
      { case: "publicKey" },
    ]);
  }
}

class P2P extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Bootstrap() {
    return new Bootstrap(this.client, this.config, [
      ...this.path,
      { case: "bootstrap" },
    ]);
  }

  Swarm() {
    return new Swarm(this.client, this.config, [
      ...this.path,
      { case: "swarm" },
    ]);
  }
}

class Bootstrap extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Shape(shapeName: string) {
    return new BootstrapShape(this.client,this.config, [
      ...this.path,
      { case: "select", shape: shapeName},
    ]);
  }

  async List(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}

class BootstrapShape extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Nodes() {
    return new StringSliceOperation(this.client, this.config, [
      ...this.path,
      { case: "nodes" },
    ]);
  }

  async Delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class Swarm extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Key() {
    return new SwarmKey(this.client, this.config, [
      ...this.path,
      { case: "key" },
    ]);
  }

  async Generate(): Promise<void> {
    await this.doRequest({ case: "generate", value: true });
  }
}

class SwarmKey extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Path() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "path" },
    ]);
  }

  Data() {
    return new BytesOperation(this.client, this.config, [
      ...this.path,
      { case: "data" },
    ]);
  }
}

// Hosts Operations
class Hosts extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage) {
    super(client, config, [{ case: "hosts" }]);
  }

  Host(name: string) {
    return new Host(this.client, this.config, [
      ...this.path,
      { case: "select", name },
    ]);
  }

  async List(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}

class Host extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Addresses() {
    return new StringSliceOperation(this.client, this.config, [
      ...this.path,
      { case: "addresses" },
    ]);
  }

  SSH() {
    return new SSH(this.client, this.config, [...this.path, { case: "ssh" }]);
  }

  Location() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "location" },
    ]);
  }

  Shapes() {
    return new HostShapes(this.client, this.config, [
      ...this.path,
      { case: "shapes" },
    ]);
  }

  async Delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class SSH extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Address() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "address" },
    ]);
  }

  Auth() {
    return new StringSliceOperation(this.client, this.config, [
      ...this.path,
      { case: "auth" },
    ]);
  }
}

class HostShapes extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Shape(shapeName: string) {
    return new HostShape(this.client, this.config, [
      ...this.path,
      { case: "select", name: shapeName },
    ]);
  }

  async List(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}

class HostShape extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Instance() {
    return new HostInstance(this.client, this.config, [
      ...this.path,
      { case: "select" },
    ]);
  }

  async Delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class HostInstance extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  async Id(): Promise<string> {
    const result = await this.doRequest({ case: "id", value: true });
    if (result.return.case === "string") {
      return result.return.value;
    }
    return "";
  }

  Key() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "key" },
    ]);
  }

  async Generate(): Promise<void> {
    await this.doRequest({ case: "generate", value: true });
  }
}

// Auth Operations
class Auth extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage) {
    super(client, config, [{ case: "auth" }]);
  }

  Signer(name: string) {
    return new Signer(this.client, this.config, [
      ...this.path,
      { case: "select", name },
    ]);
  }

  async List(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}

class Signer extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Username() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "username" },
    ]);
  }

  Password() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "password" },
    ]);
  }

  Key() {
    return new SSHKey(this.client, this.config, [
      ...this.path,
      { case: "key" },
    ]);
  }

  async Delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class SSHKey extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Path() {
    return new StringOperation(this.client, this.config, [
      ...this.path,
      { case: "path" },
    ]);
  }

  Data() {
    return new BytesOperation(this.client, this.config, [
      ...this.path,
      { case: "data" },
    ]);
  }
}

// Shapes Operations
class Shapes extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage) {
    super(client, config, [{ case: "shapes" }]);
  }

  Shape(name: string) {
    return new Shape(this.client, this.config, [
      ...this.path,
      { case: "select", name },
    ]);
  }

  async List(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}

class Shape extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Services() {
    return new StringSliceOperation(this.client, this.config, [
      ...this.path,
      { case: "services" },
    ]);
  }

  Ports() {
    return new Ports(this.client, this.config, [
      ...this.path,
      { case: "ports" },
    ]);
  }

  Plugins() {
    return new StringSliceOperation(this.client, this.config, [
      ...this.path,
      { case: "plugins" },
    ]);
  }

  async Delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class Ports extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  Port(portName: string) {
    return new Port(this.client, this.config, [
      ...this.path,
      { case: "select", name: portName },
    ]);
  }

  async List(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}

class Port extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  async Set(value: bigint): Promise<void> {
    await this.doRequest({ case: "set", value });
  }

  async Get(): Promise<bigint> {
    const result = await this.doRequest({ case: "get", value: true });
    if (result.return.case === "uint64") {
      return result.return.value;
    }
    return BigInt(0);
  }

  async Delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

// Shared Operation Classes
class StringOperation extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  async Set(value: string): Promise<void> {
    await this.doRequest({ case: "set", value });
  }

  async Get(): Promise<string> {
    const result = await this.doRequest({ case: "get", value: true });
    if (result.return.case === "string") {
      return result.return.value;
    }
    return "";
  }
}

class BytesOperation extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  async Set(value: Uint8Array): Promise<void> {
    await this.doRequest({ case: "set", value });
  }

  async Get(): Promise<Uint8Array> {
    const result = await this.doRequest({ case: "get", value: true });
    if (result.return.case === "bytes") {
      return result.return.value;
    }
    return new Uint8Array();
  }
}

class StringSliceOperation extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  async Set(values: string[]): Promise<void> {
    await this.doRequest({
      case: "set",
      value: new StringSlice({ value: values }),
    });
  }

  async Add(values: string[]): Promise<void> {
    await this.doRequest({
      case: "add",
      value: new StringSlice({ value: values }),
    });
  }

  async Delete(values: string[]): Promise<void> {
    await this.doRequest({
      case: "delete",
      value: new StringSlice({ value: values }),
    });
  }

  async Clear(): Promise<void> {
    await this.doRequest({ case: "clear", value: true });
  }

  async List(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}
