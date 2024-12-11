import { Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-node";
import { Node } from "../gen/taucorder/v1/common_pb";
import { Config } from "../gen/taucorder/v1/node_pb";
import { RPCClient as NodeRPCClient } from "./NodeClient";
import { Auth } from "./Auth";
import { GitHooks } from "./GitHooks";
import { Monkey } from "./Monkey";
import { Hoarder } from "./Hoarder";
import { Patrick } from "./Patrick";
import { Projects } from "./Projects";
import { Repositories } from "./Repositories";
import { Seer } from "./Seer";
import { Swarm } from "./Swarm";
import { TNS } from "./TNS";
import { X509 } from "./X509";
import { Health } from "./Health";

class ExtendedAuth extends Auth {
  private wrappers: {
    projects?: Projects;
    gitHooks?: GitHooks;
    repositories?: Repositories;
    x509?: X509;
  } = {};
  private transport: Transport;
  protected node: Node;

  constructor(transport: Transport, node: Node, taucorder: Taucorder) {
    super(transport, node);
    this.transport = transport;
    this.node = node;
  }

  Projects() {
    if (!this.wrappers.projects) {
      this.wrappers.projects = new Projects(this.transport, this.node);
    }
    return this.wrappers.projects;
  }

  GitHooks() {
    if (!this.wrappers.gitHooks) {
      this.wrappers.gitHooks = new GitHooks(this.transport, this.node);
    }
    return this.wrappers.gitHooks;
  }

  Repositories() {
    if (!this.wrappers.repositories) {
      this.wrappers.repositories = new Repositories(this.transport, this.node);
    }
    return this.wrappers.repositories;
  }

  X509() {
    if (!this.wrappers.x509) {
      this.wrappers.x509 = new X509(this.transport, this.node);
    }
    return this.wrappers.x509;
  }
}

export class Taucorder {
  private transport: Transport;
  private node?: Node;
  private config: Config;
  private nodeClient?: NodeRPCClient;
  private wrappers: {
    auth?: ExtendedAuth;
    monkey?: Monkey;
    hoarder?: Hoarder;
    patrick?: Patrick;
    seer?: Seer;
    swarm?: Swarm;
    tns?: TNS;
    health?: Health;
  } = {};

  constructor(rpcUrl: string, config: Config) {
    this.config = config;
    this.transport = createConnectTransport({
      baseUrl: rpcUrl,
      httpVersion: "1.1",
    });
  }

  async init() {
    if (!this.nodeClient) {
      this.nodeClient = new NodeRPCClient(this.transport);
    }
    this.node = await this.nodeClient.new(this.config);
  }

  async destroy() {
    if (this.nodeClient && this.node) {
      await this.nodeClient.free(this.node);
    }
  }

  Auth() {
    if (!this.node) throw new Error("Node not initialized");
    if (!this.wrappers.auth) {
      this.wrappers.auth = new ExtendedAuth(this.transport, this.node, this);
    }
    return this.wrappers.auth;
  }

  Monkey() {
    if (!this.node) throw new Error("Node not initialized");
    if (!this.wrappers.monkey) {
      this.wrappers.monkey = new Monkey(this.transport, this.node);
    }
    return this.wrappers.monkey;
  }

  Hoarder() {
    if (!this.node) throw new Error("Node not initialized");
    if (!this.wrappers.hoarder) {
      this.wrappers.hoarder = new Hoarder(this.transport, this.node);
    }
    return this.wrappers.hoarder;
  }

  Patrick() {
    if (!this.node) throw new Error("Node not initialized");
    if (!this.wrappers.patrick) {
      this.wrappers.patrick = new Patrick(this.transport, this.node);
    }
    return this.wrappers.patrick;
  }

  Seer() {
    if (!this.node) throw new Error("Node not initialized");
    if (!this.wrappers.seer) {
      this.wrappers.seer = new Seer(this.transport, this.node);
    }
    return this.wrappers.seer;
  }

  Swarm() {
    if (!this.node) throw new Error("Node not initialized");
    if (!this.wrappers.swarm) {
      this.wrappers.swarm = new Swarm(this.transport, this.node);
    }
    return this.wrappers.swarm;
  }

  TNS() {
    if (!this.node) throw new Error("Node not initialized");
    if (!this.wrappers.tns) {
      this.wrappers.tns = new TNS(this.transport, this.node);
    }
    return this.wrappers.tns;
  }

  Health() {
    if (!this.wrappers.health) {
      this.wrappers.health = new Health(this.transport);
    }
    return this.wrappers.health;
  }
}

export class TaucorderService extends Health {
  constructor(rpcUrl: string) {
    const transport = createConnectTransport({
      baseUrl: rpcUrl,
      httpVersion: "1.1",
    });
    super(transport);
  }

  /**
   * Wait for the service to become available by pinging until success or timeout
   * @param timeoutSeconds Maximum time to wait in seconds
   * @throws Error if service does not become available within timeout
   */
  async wait(timeoutSeconds: number): Promise<void> {
    const start = Date.now();
    const timeoutMs = timeoutSeconds * 1000;
    
    while (Date.now() - start < timeoutMs) {
      try {
        await this.ping();
        return;
      } catch (err) {
        // Wait 100ms before retrying
        await new Promise(resolve => setTimeout(resolve, 100));
      }
    }
    throw new Error(`Service did not become available within ${timeoutSeconds} seconds`);
  }
}
