// @generated by protoc-gen-es v1.4.0 with parameter "target=ts"
// @generated from file taucorder/v1/tns.proto (package taucorder.v1, syntax proto3)
/* eslint-disable */
// @ts-nocheck

import type { BinaryReadOptions, FieldList, JsonReadOptions, JsonValue, PartialMessage, PlainMessage } from "@bufbuild/protobuf";
import { Message, proto3 } from "@bufbuild/protobuf";
import { Node } from "./common_pb.js";

/**
 * Data Structures
 *
 * @generated from message taucorder.v1.TNSListRequest
 */
export class TNSListRequest extends Message<TNSListRequest> {
  /**
   * @generated from field: taucorder.v1.Node node = 1;
   */
  node?: Node;

  /**
   * @generated from field: int32 depth = 2;
   */
  depth = 0;

  constructor(data?: PartialMessage<TNSListRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "taucorder.v1.TNSListRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "node", kind: "message", T: Node },
    { no: 2, name: "depth", kind: "scalar", T: 5 /* ScalarType.INT32 */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TNSListRequest {
    return new TNSListRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TNSListRequest {
    return new TNSListRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TNSListRequest {
    return new TNSListRequest().fromJsonString(jsonString, options);
  }

  static equals(a: TNSListRequest | PlainMessage<TNSListRequest> | undefined, b: TNSListRequest | PlainMessage<TNSListRequest> | undefined): boolean {
    return proto3.util.equals(TNSListRequest, a, b);
  }
}

/**
 * @generated from message taucorder.v1.TNSFetchRequest
 */
export class TNSFetchRequest extends Message<TNSFetchRequest> {
  /**
   * @generated from field: taucorder.v1.Node node = 1;
   */
  node?: Node;

  /**
   * @generated from field: taucorder.v1.TNSPath path = 2;
   */
  path?: TNSPath;

  constructor(data?: PartialMessage<TNSFetchRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "taucorder.v1.TNSFetchRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "node", kind: "message", T: Node },
    { no: 2, name: "path", kind: "message", T: TNSPath },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TNSFetchRequest {
    return new TNSFetchRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TNSFetchRequest {
    return new TNSFetchRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TNSFetchRequest {
    return new TNSFetchRequest().fromJsonString(jsonString, options);
  }

  static equals(a: TNSFetchRequest | PlainMessage<TNSFetchRequest> | undefined, b: TNSFetchRequest | PlainMessage<TNSFetchRequest> | undefined): boolean {
    return proto3.util.equals(TNSFetchRequest, a, b);
  }
}

/**
 * @generated from message taucorder.v1.TNSLookupRequest
 */
export class TNSLookupRequest extends Message<TNSLookupRequest> {
  /**
   * @generated from field: taucorder.v1.Node node = 1;
   */
  node?: Node;

  /**
   * @generated from oneof taucorder.v1.TNSLookupRequest.match
   */
  match: {
    /**
     * @generated from field: taucorder.v1.TNSPath prefix = 2;
     */
    value: TNSPath;
    case: "prefix";
  } | {
    /**
     * @generated from field: taucorder.v1.TNSPath regex = 3;
     */
    value: TNSPath;
    case: "regex";
  } | { case: undefined; value?: undefined } = { case: undefined };

  constructor(data?: PartialMessage<TNSLookupRequest>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "taucorder.v1.TNSLookupRequest";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "node", kind: "message", T: Node },
    { no: 2, name: "prefix", kind: "message", T: TNSPath, oneof: "match" },
    { no: 3, name: "regex", kind: "message", T: TNSPath, oneof: "match" },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TNSLookupRequest {
    return new TNSLookupRequest().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TNSLookupRequest {
    return new TNSLookupRequest().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TNSLookupRequest {
    return new TNSLookupRequest().fromJsonString(jsonString, options);
  }

  static equals(a: TNSLookupRequest | PlainMessage<TNSLookupRequest> | undefined, b: TNSLookupRequest | PlainMessage<TNSLookupRequest> | undefined): boolean {
    return proto3.util.equals(TNSLookupRequest, a, b);
  }
}

/**
 * @generated from message taucorder.v1.TNSPath
 */
export class TNSPath extends Message<TNSPath> {
  /**
   * @generated from field: repeated string leafs = 1;
   */
  leafs: string[] = [];

  constructor(data?: PartialMessage<TNSPath>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "taucorder.v1.TNSPath";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "leafs", kind: "scalar", T: 9 /* ScalarType.STRING */, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TNSPath {
    return new TNSPath().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TNSPath {
    return new TNSPath().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TNSPath {
    return new TNSPath().fromJsonString(jsonString, options);
  }

  static equals(a: TNSPath | PlainMessage<TNSPath> | undefined, b: TNSPath | PlainMessage<TNSPath> | undefined): boolean {
    return proto3.util.equals(TNSPath, a, b);
  }
}

/**
 * @generated from message taucorder.v1.TNSObject
 */
export class TNSObject extends Message<TNSObject> {
  /**
   * @generated from field: taucorder.v1.TNSPath path = 1;
   */
  path?: TNSPath;

  /**
   * @generated from field: string json = 2;
   */
  json = "";

  constructor(data?: PartialMessage<TNSObject>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "taucorder.v1.TNSObject";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "path", kind: "message", T: TNSPath },
    { no: 2, name: "json", kind: "scalar", T: 9 /* ScalarType.STRING */ },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TNSObject {
    return new TNSObject().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TNSObject {
    return new TNSObject().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TNSObject {
    return new TNSObject().fromJsonString(jsonString, options);
  }

  static equals(a: TNSObject | PlainMessage<TNSObject> | undefined, b: TNSObject | PlainMessage<TNSObject> | undefined): boolean {
    return proto3.util.equals(TNSObject, a, b);
  }
}

/**
 * @generated from message taucorder.v1.TNSPaths
 */
export class TNSPaths extends Message<TNSPaths> {
  /**
   * @generated from field: repeated taucorder.v1.TNSPath paths = 1;
   */
  paths: TNSPath[] = [];

  constructor(data?: PartialMessage<TNSPaths>) {
    super();
    proto3.util.initPartial(data, this);
  }

  static readonly runtime: typeof proto3 = proto3;
  static readonly typeName = "taucorder.v1.TNSPaths";
  static readonly fields: FieldList = proto3.util.newFieldList(() => [
    { no: 1, name: "paths", kind: "message", T: TNSPath, repeated: true },
  ]);

  static fromBinary(bytes: Uint8Array, options?: Partial<BinaryReadOptions>): TNSPaths {
    return new TNSPaths().fromBinary(bytes, options);
  }

  static fromJson(jsonValue: JsonValue, options?: Partial<JsonReadOptions>): TNSPaths {
    return new TNSPaths().fromJson(jsonValue, options);
  }

  static fromJsonString(jsonString: string, options?: Partial<JsonReadOptions>): TNSPaths {
    return new TNSPaths().fromJsonString(jsonString, options);
  }

  static equals(a: TNSPaths | PlainMessage<TNSPaths> | undefined, b: TNSPaths | PlainMessage<TNSPaths> | undefined): boolean {
    return proto3.util.equals(TNSPaths, a, b);
  }
}

