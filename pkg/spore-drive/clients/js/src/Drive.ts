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

export class Drive {
  private client: RPCClient;
  private drive?: DriveMessage;

  constructor(client: RPCClient) {
    this.client = client;
  }

  async init(config: Config): Promise<void> {
    this.drive = await this.client.new(
      new DriveRequest({ config: new ConfigMessage({ id: config.id() }) })
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
  concurrency:number = 0;

  constructor(shapes:string[], concurrency?:number) {
    this.shapes = shapes;
    if (concurrency) this.concurrency = concurrency
  }
}

class Course {
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
