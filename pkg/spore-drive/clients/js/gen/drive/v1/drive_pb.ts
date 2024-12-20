// @generated by protoc-gen-es v1.4.0 with parameter "target=ts"
// @generated from file drive/v1/drive.proto (package drive.v1, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";
import { Config } from "../../config/v1/config_pb.js";

/**
 * @generated from message drive.v1.DriveRequest
 */
export class DriveRequest extends Message<DriveRequest> {
  /**
   * @generated from field: config.v1.Config config = 1;
   */
  config?: Config;

  /**
   * @generated from oneof drive.v1.DriveRequest.tau
   */
  tau: {
    /**
     * @generated from field: bool latest = 2;
     */
    value: boolean;
    case: "latest";
  } | {
    /**
     * @generated from field: string version = 3;
     */
    value: string;
    case: "version";
  } | {
    /**
     * @generated from field: string url = 4;
     */
    value: string;
    case: "url";
  } | {
    /**
     * @generated from field: string path = 5;
     */
    value: string;
    case: "path";
  } | { case: undefined; value?: undefined } = { case: undefined };

  constructor(data?: PartialMessage<DriveRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "drive.v1.DriveRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "config", kind: "message", T: Config },
    { no: 2, name: "latest", kind: "scalar", T: 8 /* ScalarType.BOOL */, oneof: "tau" },
    { no: 3, name: "version", kind: "scalar", T: 9 /* ScalarType.STRING */, oneof: "tau" },
    { no: 4, name: "url", kind: "scalar", T: 9 /* ScalarType.STRING */, oneof: "tau" },
    { no: 5, name: "path", kind: "scalar", T: 9 /* ScalarType.STRING */, oneof: "tau" },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): DriveRequest {
    return new DriveRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): DriveRequest {
    return new DriveRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): DriveRequest {
    return new DriveRequest().fromJsonString(jsonString, options);
  }

  static equals(a: DriveRequest | PlainMessage<DriveRequest> | undefined, b: DriveRequest | PlainMessage<DriveRequest> | undefined): boolean {
    return proto3.util.equals(DriveRequest, a, b);
  }
}

/**
 * @generated from message drive.v1.PlotRequest
 */
export class PlotRequest extends Message<PlotRequest> {
  /**
   * @generated from field: drive.v1.Drive drive = 1;
   */
  drive?: Drive;

  /**
   * params
   *
   * @generated from field: repeated string shapes = 2;
   */
  shapes: string[] = [];

  /**
   * @generated from field: int32 concurrency = 3;
   */
  concurrency = 0;

  constructor(data?: PartialMessage<PlotRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "drive.v1.PlotRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "drive", kind: "message", T: Drive },
    { no: 2, name: "shapes", kind: "scalar", T: 9 /* ScalarType.STRING */, repeated: true },
    { no: 3, name: "concurrency", kind: "scalar", T: 5 /* ScalarType.INT32 */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): PlotRequest {
    return new PlotRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): PlotRequest {
    return new PlotRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): PlotRequest {
    return new PlotRequest().fromJsonString(jsonString, options);
  }

  static equals(a: PlotRequest | PlainMessage<PlotRequest> | undefined, b: PlotRequest | PlainMessage<PlotRequest> | undefined): boolean {
    return proto3.util.equals(PlotRequest, a, b);
  }
}

/**
 * @generated from message drive.v1.Drive
 */
export class Drive extends Message<Drive> {
  /**
   * @generated from field: string id = 1;
   */
  id = "";

  constructor(data?: PartialMessage<Drive>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "drive.v1.Drive";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "id", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Drive {
    return new Drive().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Drive {
    return new Drive().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Drive {
    return new Drive().fromJsonString(jsonString, options);
  }

  static equals(a: Drive | PlainMessage<Drive> | undefined, b: Drive | PlainMessage<Drive> | undefined): boolean {
    return proto3.util.equals(Drive, a, b);
  }
}

/**
 * @generated from message drive.v1.Course
 */
export class Course extends Message<Course> {
  /**
   * @generated from field: string id = 1;
   */
  id = "";

  constructor(data?: PartialMessage<Course>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "drive.v1.Course";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "id", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Course {
    return new Course().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Course {
    return new Course().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Course {
    return new Course().fromJsonString(jsonString, options);
  }

  static equals(a: Course | PlainMessage<Course> | undefined, b: Course | PlainMessage<Course> | undefined): boolean {
    return proto3.util.equals(Course, a, b);
  }
}

/**
 * @generated from message drive.v1.DisplacementProgress
 */
export class DisplacementProgress extends Message<DisplacementProgress> {
  /**
   * @generated from field: string path = 1;
   */
  path = "";

  /**
   * @generated from field: string name = 2;
   */
  name = "";

  /**
   * @generated from field: int32 progress = 3;
   */
  progress = 0;

  /**
   * @generated from field: string error = 4;
   */
  error = "";

  constructor(data?: PartialMessage<DisplacementProgress>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "drive.v1.DisplacementProgress";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "path", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 2, name: "name", kind: "scalar", T: 9 /* ScalarType.STRING */ },
    { no: 3, name: "progress", kind: "scalar", T: 5 /* ScalarType.INT32 */ },
    { no: 4, name: "error", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): DisplacementProgress {
    return new DisplacementProgress().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): DisplacementProgress {
    return new DisplacementProgress().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): DisplacementProgress {
    return new DisplacementProgress().fromJsonString(jsonString, options);
  }

  static equals(a: DisplacementProgress | PlainMessage<DisplacementProgress> | undefined, b: DisplacementProgress | PlainMessage<DisplacementProgress> | undefined): boolean {
    return proto3.util.equals(DisplacementProgress, a, b);
  }
}

/**
 * @generated from message drive.v1.Empty
 */
export class Empty extends Message<Empty> {
  constructor(data?: PartialMessage<Empty>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "drive.v1.Empty";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): Empty {
    return new Empty().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): Empty {
    return new Empty().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): Empty {
    return new Empty().fromJsonString(jsonString, options);
  }

  static equals(a: Empty | PlainMessage<Empty> | undefined, b: Empty | PlainMessage<Empty> | undefined): boolean {
    return proto3.util.equals(Empty, a, b);
  }
}

