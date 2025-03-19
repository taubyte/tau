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

  get id(): string | undefined {
    return this.config?.id
  }

  get cloud() : Cloud {
    return new Cloud(this.client, this.config!);
  }

  get hosts(): Hosts {
    return new Hosts(this.client, this.config!);
  }

  get host(): Record<string, Host> {
    return new Proxy({}, {
      get: (target, name: string): Host => {
        return new Hosts(this.client, this.config!).get(name);
      },
    });
  }

  get auth(): Auth {
    return new Auth(this.client, this.config!);
  }

  async shapes(): Promise<string[]> {
    return await new Shapes(this.client, this.config!).list();
  }

  get shape(): Record<string, Shape> {
    return new Proxy({}, {
      get: (target, name: string): Shape => {
        return new Shapes(this.client, this.config!).get(name);
      },
    });
  }

  async commit(): Promise<Empty> {
    if (!this.config) throw new Error("Config not loaded.");
    return await this.client.commit(this.config);
  }

  async download(): Promise<AsyncIterable<Bundle>> {
    return await this.client.download(new BundleConfig({ id: this.config }));
  }
}

// Base Operation class to hold the path
class BaseOperation {
  protected client: RPCClient;
  protected config: ConfigMessage;
  protected opPath: any[];

  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    this.client = client;
    this.config = config;
    this.opPath = path;
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
      for (let i = this.opPath.length - 1; i >= 0; i--) {
      const pathItem = this.opPath[i];
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

  get domain() : Domain {
    return new Domain(this.client, this.config, [
      ...this.opPath,
      { case: "domain" },
    ]);
  }

  get p2p() : P2P {
    return new P2P(this.client, this.config, [...this.opPath, { case: "p2p" }]);
  }
}

class Domain extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get root() {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "root" },
    ]);
  }

  get generated() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "generated" },
    ]);
  }

  get validation() : Validation {
    return new Validation(this.client, this.config, [
      ...this.opPath,
      { case: "validation" },
    ]);
  }
}

class Validation extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get keys() : ValidationKeys {
    return new ValidationKeys(this.client, this.config, [
      ...this.opPath,
      { case: "keys" },
    ]);
  }

  async generate(): Promise<void> {
    await this.doRequest({ case: "generate", value: true });
  }
}

class ValidationKeys extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get path() : ValidationKeysPath {
    return new ValidationKeysPath(this.client, this.config, [
      ...this.opPath,
      { case: "path" },
    ]);
  }

  get data() : ValidationKeysData {
    return new ValidationKeysData(this.client, this.config, [
      ...this.opPath,
      { case: "data" },
    ]);
  }
}

class ValidationKeysPath extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  } 

  get privateKey() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "privateKey" },
    ]);
  }

  get publicKey() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "publicKey" },
    ]);
  }
}

class ValidationKeysData extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get privateKey() : BytesOperation {
    return new BytesOperation(this.client, this.config, [
      ...this.opPath,
      { case: "privateKey" },
    ]);
  }

  get publicKey() : BytesOperation {
    return new BytesOperation(this.client, this.config, [
      ...this.opPath,
      { case: "publicKey" },
    ]);
  }
}

class P2P extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get bootstrap() : Bootstrap {
    return new Bootstrap(this.client, this.config, [
      ...this.opPath,
      { case: "bootstrap" },
    ]);
  }

  get swarm() : Swarm {
    return new Swarm(this.client, this.config, [
      ...this.opPath,
      { case: "swarm" },
    ]);
  }
}

class Bootstrap extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get shape(): Record<string, BootstrapShape> {
    return new Proxy({}, {
      get: (target, shapeName: string): BootstrapShape => {
        return new BootstrapShape(this.client, this.config, [
          ...this.opPath,
          { case: "select", shape: shapeName },
        ]);
      },
    });
  }

  async list(): Promise<string[]> {
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

  get nodes() : StringSliceOperation {
    return new StringSliceOperation(this.client, this.config, [
      ...this.opPath,
      { case: "nodes" },
    ]);
  }

  async delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class Swarm extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get key() : SwarmKey {
    return new SwarmKey(this.client, this.config, [
      ...this.opPath,
      { case: "key" },
    ]);
  }

  async generate(): Promise<void> {
    await this.doRequest({ case: "generate", value: true });
  }
}

class SwarmKey extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get path() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "path" },
    ]);
  }

  get data() : BytesOperation {
    return new BytesOperation(this.client, this.config, [
      ...this.opPath,
      { case: "data" },
    ]);
  }
}

// Hosts Operations
class Hosts extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage) {
    super(client, config, [{ case: "hosts" }]);
  }

  get(name: string): Host {
    return new Host(this.client, this.config, [
          ...this.opPath,
      { case: "select", name },
    ]);
  }

  async list(): Promise<string[]> {
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

  get addresses() : StringSliceOperation {
    return new StringSliceOperation(this.client, this.config, [
      ...this.opPath,
      { case: "addresses" },
    ]);
  }

  get ssh() : SSH {
    return new SSH(this.client, this.config, [...this.opPath, { case: "ssh" }]);
  }

  get location() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "location" },
    ]);
  }

  get shapes() : HostShapes {
    return new HostShapes(this.client, this.config, [
      ...this.opPath,
      { case: "shapes" },
    ]);
  }

  get shape(): Record<string, HostShape> {
    return new Proxy({}, {
      get: (target, shapeName: string): HostShape => {
        return new HostShapes(this.client, this.config, [
          ...this.opPath,
          { case: "shapes" },
        ]).get(shapeName);
      },
    });
  }

  async delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class SSH extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get address() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "address" },
    ]);
  }

  get auth() : StringSliceOperation {
    return new StringSliceOperation(this.client, this.config, [
      ...this.opPath,
      { case: "auth" },
    ]);
  }
}

class HostShapes extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get(name: string): HostShape {
    return new HostShape(this.client, this.config, [
      ...this.opPath,
      { case: "select", name },
    ]);
  }

  async list(): Promise<string[]> {
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

  get instance() : HostInstance {
    return new HostInstance(this.client, this.config, [
      ...this.opPath,
      { case: "select" },
    ]);
  }

  async delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class HostInstance extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  async id() {
    const result = await this.doRequest({ case: "id", value: true });
    if (result.return.case === "string") {
      return result.return.value;
    }
    return "";
  }

  get key() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "key" },
    ]);
  }

  async generate(): Promise<void> {
    await this.doRequest({ case: "generate", value: true });
  }
}

// Auth Operations
class Auth extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage) {
    super(client, config, [{ case: "auth" }]);
  }

  get signer(): Record<string, Signer> {
    return new Proxy({}, {
      get: (target, name: string): Signer => {
        return new Signer(this.client, this.config, [
          ...this.opPath,
          { case: "select", name },
        ]);
      },
    });
  }

  async list(): Promise<string[]> {
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

  get username() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "username" },
    ]);
  }

  get password() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "password" },
    ]);
  }

  get key() : SSHKey {
    return new SSHKey(this.client, this.config, [
      ...this.opPath,
      { case: "key" },
    ]);
  }

  async delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class SSHKey extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get path() : StringOperation {
    return new StringOperation(this.client, this.config, [
      ...this.opPath,
      { case: "path" },
    ]);
  }

  get data() : BytesOperation {
    return new BytesOperation(this.client, this.config, [
      ...this.opPath,
      { case: "data" },
    ]);
  }
}

// Shapes Operations
class Shapes extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage) {
    super(client, config, [{ case: "shapes" }]);
  }

  get(name: string): Shape {
    return new Shape(this.client, this.config, [
      ...this.opPath,
      { case: "select", name },
    ]);
  }

  async list(): Promise<string[]> {
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

  get services() : StringSliceOperation {
    return new StringSliceOperation(this.client, this.config, [
      ...this.opPath,
      { case: "services" },
    ]);
  }

  get ports() : Ports {
    return new Ports(this.client, this.config, [
      ...this.opPath,
      { case: "ports" },
    ]);
  }

  get plugins() : StringSliceOperation {
    return new StringSliceOperation(this.client, this.config, [
      ...this.opPath,
      { case: "plugins" },
    ]);
  }

  async delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

class Ports extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  get port(): Record<string, Port> {
    return new Proxy({}, {
      get: (target, portName: string): Port => {
        return new Port(this.client, this.config, [
          ...this.opPath,
          { case: "select", name: portName },
        ]);
      },
    });
  }

  async list(): Promise<string[]> {
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

  async set(value: bigint): Promise<void> {
    await this.doRequest({ case: "set", value });
  }

  async get(): Promise<bigint> {
    const result = await this.doRequest({ case: "get", value: true });
    if (result.return.case === "uint64") {
      return result.return.value;
    }
    return BigInt(0);
  }

  async delete(): Promise<void> {
    await this.doRequest({ case: "delete", value: true });
  }
}

// Shared Operation Classes
class StringOperation extends BaseOperation {
  constructor(client: RPCClient, config: ConfigMessage, path: any[]) {
    super(client, config, path);
  }

  async set(value: string): Promise<void> {
    await this.doRequest({ case: "set", value });
  }

  async get(): Promise<string> {
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

  async set(value: Uint8Array): Promise<void> {
    await this.doRequest({ case: "set", value });
  }

  async get(): Promise<Uint8Array> {
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

  async set(values: string[]): Promise<void> {
    await this.doRequest({
      case: "set",
      value: new StringSlice({ value: values }),
    });
  }

  async add(values: string[]): Promise<void> {
    await this.doRequest({
      case: "add",
      value: new StringSlice({ value: values }),
    });
  }

  async delete(values: string[]): Promise<void> {
    await this.doRequest({
      case: "delete",
      value: new StringSlice({ value: values }),
    });
  }

  async clear(): Promise<void> {
    await this.doRequest({ case: "clear", value: true });
  }

  async list(): Promise<string[]> {
    const result = await this.doRequest({ case: "list", value: true });
    if (result.return.case === "slice") {
      return result.return.value.value;
    }
    return [];
  }
}
