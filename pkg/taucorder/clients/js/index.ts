import {
  Node,
  Empty,
  Job,
  Peer,
  Peers,
  ConsensusState,
  RepositoryId,
} from "./gen/taucorder/v1/common_pb";
import { Config } from "./gen/taucorder/v1/node_pb";
import { StashedItem, StashRequest } from "./gen/taucorder/v1/hoarder_pb";
import {
  Project,
  ProjectRepo,
  Hook,
  X509Certificate,
} from "./gen/taucorder/v1/auth_pb";
import {
  PeerUsage,
  PeerLocation,
  Location,
  LocationArea,
} from "./gen/taucorder/v1/seer_pb";
import { TNSPath, TNSObject, TNSPaths } from "./gen/taucorder/v1/tns_pb";
import { Taucorder as Core } from "./src/Taucorder";
import { Service } from "./src/Service";

export {
  Node,
  Empty,
  Job,
  Peer,
  Peers,
  ConsensusState,
  RepositoryId,
  Config,
  StashedItem,
  StashRequest,
  Project,
  ProjectRepo,
  Hook,
  X509Certificate,
  PeerUsage,
  PeerLocation,
  Location,
  LocationArea,
  TNSPath,
  TNSObject,
  TNSPaths,
};

export class Taucorder extends Core {
  private service: Service;

  constructor(rpcUrl: string, config: Config) {
    super(rpcUrl, config);
    this.service = new Service();
  }

  public async init(): Promise<void> {
    await this.service.run();
    await super.init();
  }
}
