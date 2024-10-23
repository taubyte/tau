import { Service } from "./Service";
import { Config as BaseConfig } from "./Config";
import {
  Drive as BaseDrive,
  CourseConfig,
  TauBinarySource,
  TauLatest,
  TauVersion,
  TauUrl,
  TauPath,
} from "./Drive";

class Config extends BaseConfig {
  private service: Service;

  constructor(source?: string | ReadableStream<Uint8Array>) {
    super(source);
    this.service = new Service();
  }

  public async init(): Promise<void> {
    await this.service.run();
    await super.init(`http://localhost:${this.service.getPort()}/`);
  }
}

class Drive extends BaseDrive {
  private service: Service;

  constructor(config: Config) {
    super(config);
    this.service = new Service();
  }

  public async init(): Promise<void> {
    await this.service.run();
    await super.init(`http://localhost:${this.service.getPort()}/`);
  }
}

export {
  Config,
  Drive,
  CourseConfig,
  TauBinarySource,
  TauLatest,
  TauVersion,
  TauUrl,
  TauPath,
};
