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
    await super.init(`http://localhost:${this.service.getPort()}/`);
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
    await super.init(`http://localhost:${this.service.getPort()}/`);
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
