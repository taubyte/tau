import { Transport } from "@connectrpc/connect";
import { RPCClient } from "./SeerClient";
import { Node, Peer,Peers } from "../gen/taucorder/v1/common_pb";
import {
  NodesListRequest,
  NodesUsageRequest,
  LocationRequest,
  PeerUsage,
  PeerLocation,
  Location,
  LocationArea,
} from "../gen/taucorder/v1/seer_pb";

export type LocationFilter =
  | {
      type: "area";
      location: { latitude: number; longitude: number };
      distance: number;
    }
  | { type: "peers"; peerIds: string[] }
  | { type: "all" };

export class Seer {
  private client: RPCClient;
  private node: Node;

  constructor(transport: Transport, node: Node) {
    this.client = new RPCClient(transport);
    this.node = node;
  }

  /**
   * List nodes
   * @param service Service name
   * @returns AsyncIterable of peers
   */
  async list(service: string): Promise<AsyncIterable<Peer>> {
    const request = new NodesListRequest({
      node: this.node,
      service: service,
    });
    return this.client.list(request);
  }

  /**
   * Get node usage
   * @param peerId Peer ID
   * @returns Peer usage
   */
  async usage(peerId: string): Promise<PeerUsage> {
    const request = new NodesUsageRequest({
      node: this.node,
      peer: peerId,
    });
    return this.client.usage(request);
  }

  /**
   * Get node locations with area filter
   * @param location Location coordinates
   * @param distance Distance in meters
   * @returns AsyncIterable of peer locations
   */

  /**
   * Get node locations based on filter
   * @param filter Location filter - can be area coordinates+distance, specific peer IDs, or all nodes
   * @returns AsyncIterable of peer locations
   */
  async location(filter: LocationFilter): Promise<AsyncIterable<PeerLocation>> {
    let requestFilter: LocationRequest["filter"];

    switch (filter.type) {
      case "area": {
        const locationObj = new Location({
          latitude: filter.location.latitude,
          longitude: filter.location.longitude,
        });
        const area = new LocationArea({
          location: locationObj,
          distance: filter.distance,
        });
        requestFilter = {
          case: "area" as const,
          value: area,
        };
        break;
      }
      case "peers": {
        const peers = new Peers({
          pids: filter.peerIds,
        });
        requestFilter = {
          case: "peers" as const,
          value: peers,
        };
        break;
      }
      case "all":
        requestFilter = {
          case: "all" as const,
          value: true,
        };
        break;
    }

    const request = new LocationRequest({
      node: this.node,
      filter: requestFilter,
    });
    return this.client.location(request);
  }
}
