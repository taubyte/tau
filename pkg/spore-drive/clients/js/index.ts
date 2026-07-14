import { Service } from "./src/Service";
import { Config as BaseConfig } from "./src/Config";
import {
  Drive as BaseDrive,
  CourseConfig,
  TauBinarySource,
  TauLatest,
  TauVersion,
  TauUrl,
  TauPath,
  Course,
} from "./src/Drive";

class Config extends BaseConfig {
  private service: Service;

  constructor(source?: string | ReadableStream<Uint8Array>) {
    super(source);
    this.service = new Service();
  }

  public async init(): Promise<void> {
    await this.service.run();
    await super.init(`http://localhost:${await this.service.getPort()}/`);
  }
}

class Drive extends BaseDrive {
  private service: Service;

  constructor(config: Config, tau?: TauBinarySource) {
    super(config, tau);
    this.service = new Service();
  }

  public async init(): Promise<void> {
    await this.service.run();
    await super.init(`http://localhost:${await this.service.getPort()}/`);
  }
}

export {
  Config,
  Drive,
  Course,
  CourseConfig,
  TauBinarySource,
  TauLatest,
  TauVersion,
  TauUrl,
  TauPath,
  Service,
};

// Extension surface for building on the community client (e.g. the ee client):
// the connection-level Config (no bundled Service), the reusable op-machinery,
// and the RPC client that owns the transport.
export { Config as RemoteConfig } from "./src/Config";
export {
  BaseOperation,
  StringOperation,
  UInt64Operation,
  StringSliceOperation,
  BoolOperation,
} from "./src/Config";
export { RPCClient } from "./src/ConfigClient";
export type { OpClient } from "./src/ConfigClient";
