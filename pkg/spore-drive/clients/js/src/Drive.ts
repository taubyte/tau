import {
  DriveRequest,
  PlotRequest,
  Drive as DriveMessage,
  Course as CourseMessage,
  DisplacementProgress,
} from "../gen/drive/v1/drive_pb";

import { Config as ConfigMessage } from "../gen/config/v1/config_pb";

import { RPCClient } from "./DriveClient";

import { Config } from "./Config";

type TauBinarySource =
  | {
      value: boolean;
      case: "latest";
    }
  | {
      value: string;
      case: "version";
    }
  | {
      value: string;
      case: "url";
    }
  | {
      value: string;
      case: "path";
    };

const TauLatest:TauBinarySource = { value: true, case: "latest" };
const TauVersion = (version: string):TauBinarySource => ({ value: version, case: "version" });
const TauUrl = (url: string):TauBinarySource => ({ value: url, case: "url" });
const TauPath = (path: string):TauBinarySource => ({ value: path, case: "path" });

export { TauBinarySource, TauLatest, TauVersion, TauUrl, TauPath };

export class Drive {
  private client!: RPCClient;
  private drive?: DriveMessage;
  private config: Config;
  private tau?: TauBinarySource;

  constructor(config: Config,tau?: TauBinarySource) {
    this.config = config;
    this.tau = tau;
  }

  async init(url: string): Promise<void> {
    this.client = new RPCClient(url);
    this.drive = await this.client.new(
      new DriveRequest({
        config: new ConfigMessage({ id: this.config.id() }),
        tau: this.tau,
      })
    );
  }

  async free(): Promise<void> {
    if (this.drive) {
      await this.client.free(this.drive);
    }
  }

  async plot(config: CourseConfig): Promise<Course> {
    if (!this.drive) {
      throw new Error("drive has not been initialized");
    }
    return new Course(this.client, this.drive as DriveMessage, config).init();
  }
}

export class CourseConfig {
  shapes: string[] = [];
  concurrency: number = 0;

  constructor(shapes: string[], concurrency?: number) {
    this.shapes = shapes;
    if (concurrency) this.concurrency = concurrency;
  }
}

export class Course {
  private client: RPCClient;
  private drive: DriveMessage;
  private course?: CourseMessage;
  private config: CourseConfig;

  constructor(client: RPCClient, drive: DriveMessage, config: CourseConfig) {
    this.client = client;
    this.drive = drive;
    this.config = config;
  }

  async init(): Promise<Course> {
    this.course = await this.client.plot(
      new PlotRequest({
        drive: this.drive,
        shapes: this.config.shapes,
        concurrency: this.config.concurrency,
      })
    );
    return this;
  }

  async displace(): Promise<void> {
    if (!this.course) {
      throw new Error("course has not been initialized");
    }
    await this.client.displace(this.course);
  }

  async progress(): Promise<AsyncIterable<DisplacementProgress>> {
    if (!this.course) {
      throw new Error("course has not been initialized");
    }
    return await this.client.progress(this.course);
  }

  async abort(): Promise<void> {
    if (this.course) {
      await this.client.abort(this.course);
    }
  }
}
